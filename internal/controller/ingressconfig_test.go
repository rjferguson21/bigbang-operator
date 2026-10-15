package controller_test

import (
	"context"
	"fmt"
	"testing"

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

const generationFailedReason = "GenerationFailed"

func TestReconcile_GlobalIngressConfig(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, globalConfigNS)
	te.ensureNamespace(t, "rec-ingress")
	ctx := context.Background()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: globalConfigNS, Name: globalConfigName},
		Data: map[string]string{
			"routes":   "defaults: {inbound: {gateways: [edge/public]}}",
			"gateways": "edge/public: {podSelector: {matchLabels: {app: custom}}, serviceAccountName: gateway-sa}",
		},
	}
	mustCreate(t, te.k8s, cm)
	var pkg bbv1alpha1.Package
	if err := yaml.UnmarshalStrict([]byte(`metadata: {name: app, namespace: rec-ingress}
spec:
  istio: {enabled: true, ambient: {enabled: true}}
  networkPolicies: {enabled: true}
  routes:
    inbound:
      web: {enabled: true, service: web, port: 8080, hosts: [web.example.com]}
`), &pkg); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, te.k8s, &pkg)
	check := func(gateway, selector, sa string) {
		t.Helper()
		waitFor(t, func() error {
			var vs istionetv1.VirtualService
			if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: "web"}, &vs); err != nil {
				return err
			}
			if len(vs.Spec.Gateways) != 1 || vs.Spec.Gateways[0] != "edge/"+gateway {
				return fmt.Errorf("gateways = %v", vs.Spec.Gateways)
			}
			var np networkingv1.NetworkPolicy
			if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: "allow-ingress-to-web-8080-from-ns-edge-pod-" + gateway}, &np); err != nil {
				return err
			}
			if np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app"] != selector {
				return fmt.Errorf("selector not updated")
			}
			var ap istiosecv1.AuthorizationPolicy
			if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: "web-" + gateway + "-authz-policy"}, &ap); err != nil {
				return err
			}
			if ap.Spec.Rules[0].From[0].Source.Principals[0] != "cluster.local/ns/edge/sa/"+sa {
				return fmt.Errorf("principal not updated")
			}
			return nil
		})
	}
	update := func(routes, gateways string) {
		t.Helper()
		var current corev1.ConfigMap
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: globalConfigNS, Name: globalConfigName}, &current); err != nil {
			t.Fatal(err)
		}
		current.Data["routes"], current.Data["gateways"] = routes, gateways
		if err := te.k8s.Update(ctx, &current); err != nil {
			t.Fatal(err)
		}
	}
	check("public", "custom", "gateway-sa")
	update(cm.Data["routes"], "edge/public: {podSelector: {matchLabels: {app: changed}}, serviceAccountName: changed-sa}")
	check("public", "changed", "changed-sa")
	update("defaults: {inbound: {gateways: [edge/internal]}}", "edge/internal: {podSelector: {matchLabels: {app: internal}}, serviceAccountName: internal-sa}")
	check("internal", "internal", "internal-sa")
	waitFor(t, func() error {
		var old istiosecv1.AuthorizationPolicy
		err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: "web-public-authz-policy"}, &old)
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("old gateway authorization policy not pruned: %v", err)
	})
	update(cm.Data["routes"], "edge/public: {serviceAccountName: missing-selector}")
	waitFor(t, func() error {
		var current bbv1alpha1.Package
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: pkg.Name}, &current); err != nil {
			return err
		}
		ready := meta.FindStatusCondition(current.Status.Conditions, "Ready")
		if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != generationFailedReason {
			return fmt.Errorf("expected GenerationFailed")
		}
		return nil
	})
	// Invalid config must not replace the last working policies with legacy identities.
	check("internal", "internal", "internal-sa")
	update("defaults: {inbound: {gateways: [edge/internal]}}", "edge/internal: {podSelector: {matchLabels: {app: restored}}, serviceAccountName: restored-sa}")
	check("internal", "restored", "restored-sa")

	// Package defaults survive API storage and override the global gateway list.
	var current bbv1alpha1.Package
	if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: pkg.Name}, &current); err != nil {
		t.Fatal(err)
	}
	current.Spec.Routes.Defaults = &bbv1alpha1.RouteDefaults{Inbound: &bbv1alpha1.InboundRouteDefaults{Gateways: []string{"edge/public"}}}
	if err := te.k8s.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	update(cm.Data["routes"], cm.Data["gateways"])
	check("public", "custom", "gateway-sa")

	// An explicit empty Package list must survive serialization and must not
	// inherit the non-empty global list.
	if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: pkg.Name}, &current); err != nil {
		t.Fatal(err)
	}
	current.Spec.Routes.Defaults.Inbound.Gateways = []string{}
	if err := te.k8s.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() error {
		var got bbv1alpha1.Package
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: pkg.Namespace, Name: pkg.Name}, &got); err != nil {
			return err
		}
		ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
		if got.Spec.Routes.Defaults.Inbound.Gateways == nil || ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != generationFailedReason {
			return fmt.Errorf("empty gateway override was not retained or rejected")
		}
		return nil
	})
}
