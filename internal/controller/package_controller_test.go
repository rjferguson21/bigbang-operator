/*
Copyright 2026 Big Bang.
*/

package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

const (
	condReady       = "Ready"
	condStalled     = "Stalled"
	condReconciling = "Reconciling"
)

func rawJSON(s string) apiextensionsv1.JSON {
	return apiextensionsv1.JSON{Raw: []byte(s)}
}

// TestReconcile_DefaultsApplied creates a Package with the minimum spec
// the headline path uses and asserts the reconciler emits the default
// PeerAuthentication + 7 NetworkPolicies, owner-stamped and labeled.
func TestReconcile_DefaultsApplied(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, "rec-defaults")
	ctx := context.Background()

	pkg := newPackage("example-app", "rec-defaults", func(p *bbv1alpha1.Package) {
		p.Spec.Istio = &bbv1alpha1.Istio{Enabled: true}
		p.Spec.NetworkPolicies = &bbv1alpha1.NetworkPolicies{Enabled: true}
	})
	mustCreate(t, te.k8s, pkg)

	waitFor(t, func() error {
		var nps networkingv1.NetworkPolicyList
		if err := te.k8s.List(ctx, &nps,
			client.InNamespace("rec-defaults"),
			client.MatchingLabels{"bigbang.dev/package": "example-app"}); err != nil {
			return err
		}
		if len(nps.Items) < 7 {
			return fmt.Errorf("want >=7 NetworkPolicies, got %d", len(nps.Items))
		}
		return nil
	})

	// PeerAuthentication should also be present.
	pa := &istiosecv1.PeerAuthentication{}
	waitFor(t, func() error {
		return te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-defaults", Name: "default-peer-auth"}, pa)
	})

	// Each emitted object should carry the owner ref + prune label.
	if got := pa.Labels["bigbang.dev/package"]; got != "example-app" {
		t.Errorf("PeerAuth label bigbang.dev/package = %q, want %q", got, "example-app")
	}
	if len(pa.OwnerReferences) == 0 || pa.OwnerReferences[0].Kind != "Package" {
		t.Errorf("PeerAuth missing Package owner ref, got %#v", pa.OwnerReferences)
	}

	// Status should land Ready=True with observedGeneration set.
	waitFor(t, func() error {
		var got bbv1alpha1.Package
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-defaults", Name: "example-app"}, &got); err != nil {
			return err
		}
		if got.Status.ObservedGeneration != got.Generation {
			return fmt.Errorf("observedGeneration %d != generation %d", got.Status.ObservedGeneration, got.Generation)
		}
		ready := false
		for _, c := range got.Status.Conditions {
			switch c.Type {
			case condReady:
				ready = c.Status == metav1.ConditionTrue
			case condStalled, condReconciling:
				// kstatus abnormal-true conditions must be absent when healthy.
				return fmt.Errorf("%s condition present on healthy Package", c.Type)
			}
		}
		if !ready {
			return fmt.Errorf("Ready=True condition not found")
		}
		return nil
	})
}

// TestReconcile_StalledOnFailure drives a generation failure (unknown egress
// definition) and asserts the kstatus contract: Stalled=True, Ready=False,
// observedGeneration stamped even though the apply never happened. Fixing
// the spec must clear Stalled and land Ready=True.
func TestReconcile_StalledOnFailure(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, "rec-stalled")
	ctx := context.Background()

	pkg := newPackage("example-app", "rec-stalled", func(p *bbv1alpha1.Package) {
		p.Spec.NetworkPolicies = &bbv1alpha1.NetworkPolicies{
			Enabled: true,
			Egress: &bbv1alpha1.NetworkPoliciesEgress{
				From: map[string]apiextensionsv1.JSON{
					"example-app": rawJSON(`{"to":{"definition":{"noSuchDefinition":true}}}`),
				},
			},
		}
	})
	mustCreate(t, te.k8s, pkg)

	waitFor(t, func() error {
		var got bbv1alpha1.Package
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-stalled", Name: "example-app"}, &got); err != nil {
			return err
		}
		if got.Status.ObservedGeneration != got.Generation {
			return fmt.Errorf("observedGeneration %d != generation %d", got.Status.ObservedGeneration, got.Generation)
		}
		var stalled, notReady bool
		for _, c := range got.Status.Conditions {
			if c.Type == condStalled && c.Status == metav1.ConditionTrue && c.Reason == "GenerationFailed" {
				stalled = true
			}
			if c.Type == condReady && c.Status == metav1.ConditionFalse {
				notReady = true
			}
		}
		if !stalled || !notReady {
			return fmt.Errorf("want Stalled=True and Ready=False, got %+v", got.Status.Conditions)
		}
		return nil
	})

	// Fix the spec: Stalled must clear and Ready flip to True.
	mustUpdate(t, te.k8s, "rec-stalled", "example-app", func(p *bbv1alpha1.Package) {
		p.Spec.NetworkPolicies.Egress = nil
	})

	waitFor(t, func() error {
		var got bbv1alpha1.Package
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-stalled", Name: "example-app"}, &got); err != nil {
			return err
		}
		if got.Status.ObservedGeneration != got.Generation {
			return fmt.Errorf("observedGeneration %d != generation %d", got.Status.ObservedGeneration, got.Generation)
		}
		ready := false
		for _, c := range got.Status.Conditions {
			switch c.Type {
			case condReady:
				ready = c.Status == metav1.ConditionTrue
			case condStalled, condReconciling:
				return fmt.Errorf("%s condition still present after recovery", c.Type)
			}
		}
		if !ready {
			return fmt.Errorf("Ready=True condition not found after recovery")
		}
		return nil
	})
}

// TestReconcile_PruneOnSpecShrink verifies the label-driven prune sweep
// deletes an additionalPolicies[] entry that's removed from spec.
func TestReconcile_PruneOnSpecShrink(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, "rec-prune")
	ctx := context.Background()

	pkg := newPackage("example-app", "rec-prune", func(p *bbv1alpha1.Package) {
		p.Spec.Istio = &bbv1alpha1.Istio{Enabled: true}
		p.Spec.NetworkPolicies = &bbv1alpha1.NetworkPolicies{
			Enabled: true,
			AdditionalPolicies: []bbv1alpha1.AdditionalPolicy{{
				Name: "allow-external-egress",
				Spec: bbv1alpha1.AdditionalPolicySpec{
					"podSelector": rawJSON(`{"matchLabels":{"app":"x"}}`),
					"policyTypes": rawJSON(`["Egress"]`),
				},
			}},
		}
	})
	mustCreate(t, te.k8s, pkg)

	// Wait for the extra NetworkPolicy to land.
	waitFor(t, func() error {
		np := &networkingv1.NetworkPolicy{}
		return te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-prune", Name: "allow-external-egress"}, np)
	})

	// Remove additionalPolicies and re-apply.
	mustUpdate(t, te.k8s, "rec-prune", "example-app", func(p *bbv1alpha1.Package) {
		p.Spec.NetworkPolicies.AdditionalPolicies = nil
	})

	// The label-driven sweep should delete it.
	waitFor(t, func() error {
		np := &networkingv1.NetworkPolicy{}
		err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-prune", Name: "allow-external-egress"}, np)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err == nil {
			return fmt.Errorf("allow-external-egress still present, prune did not run")
		}
		return err
	})

	// Defaults should still be there.
	denyAll := &networkingv1.NetworkPolicy{}
	if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-prune", Name: "default-egress-deny-all"}, denyAll); err != nil {
		t.Fatalf("default-egress-deny-all gone after prune: %v", err)
	}
}

// TestReconcile_DriftRecovery deletes a managed NetworkPolicy out from
// under the operator and asserts Owns() drives a re-apply.
func TestReconcile_DriftRecovery(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, "rec-drift")
	ctx := context.Background()

	pkg := newPackage("example-app", "rec-drift", func(p *bbv1alpha1.Package) {
		p.Spec.Istio = &bbv1alpha1.Istio{Enabled: true}
		p.Spec.NetworkPolicies = &bbv1alpha1.NetworkPolicies{Enabled: true}
	})
	mustCreate(t, te.k8s, pkg)

	target := types.NamespacedName{Namespace: "rec-drift", Name: "default-egress-deny-all"}
	waitFor(t, func() error {
		np := &networkingv1.NetworkPolicy{}
		return te.k8s.Get(ctx, target, np)
	})

	// Delete it; Owns() should trigger a reconcile that re-creates it.
	np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: target.Namespace, Name: target.Name}}
	if err := te.k8s.Delete(ctx, np); err != nil {
		t.Fatalf("delete: %v", err)
	}

	waitFor(t, func() error {
		got := &networkingv1.NetworkPolicy{}
		return te.k8s.Get(ctx, target, got)
	})
}

// TestReconcile_KubeAPIDefinitionPorts verifies the controller resolves the
// built-in kubeAPI egress definition's ports from the default/kubernetes
// Service (bb-common does the same via a render-time lookup).
func TestReconcile_KubeAPIDefinitionPorts(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, "rec-kubeapi")
	ctx := context.Background()

	var apiSvc corev1.Service
	if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "default", Name: "kubernetes"}, &apiSvc); err != nil {
		t.Fatalf("get default/kubernetes Service: %v", err)
	}
	if len(apiSvc.Spec.Ports) == 0 {
		t.Fatal("default/kubernetes Service has no ports")
	}

	pkg := newPackage("example-app", "rec-kubeapi", func(p *bbv1alpha1.Package) {
		p.Spec.NetworkPolicies = &bbv1alpha1.NetworkPolicies{
			Enabled: true,
			Egress: &bbv1alpha1.NetworkPoliciesEgress{
				From: map[string]apiextensionsv1.JSON{
					"example-app": rawJSON(`{"to":{"definition":{"kubeAPI":true}}}`),
				},
			},
		}
	})
	mustCreate(t, te.k8s, pkg)

	waitFor(t, func() error {
		var np networkingv1.NetworkPolicy
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-kubeapi", Name: "allow-egress-from-example-app-to-kubeapi"}, &np); err != nil {
			return err
		}
		if len(np.Spec.Egress) != 1 {
			return fmt.Errorf("want 1 egress rule, got %d", len(np.Spec.Egress))
		}
		ports := np.Spec.Egress[0].Ports
		if len(ports) != len(apiSvc.Spec.Ports) {
			return fmt.Errorf("want %d ports (from default/kubernetes), got %d", len(apiSvc.Spec.Ports), len(ports))
		}
		want := apiSvc.Spec.Ports[0].TargetPort.String()
		if got := ports[0].Port.String(); got != want {
			return fmt.Errorf("port = %s, want targetPort %s", got, want)
		}
		return nil
	})
}

// TestReconcile_DefaultsAsHooksWarning verifies a Package that enables the
// (Helm-only) defaultsAsHooks knob gets a Warning event instead of a silent
// no-op.
func TestReconcile_DefaultsAsHooksWarning(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, "rec-hooks")
	ctx := context.Background()

	enabled := true
	pkg := newPackage("hooks-app", "rec-hooks", func(p *bbv1alpha1.Package) {
		p.Spec.NetworkPolicies = &bbv1alpha1.NetworkPolicies{
			Enabled:         true,
			DefaultsAsHooks: &bbv1alpha1.NetworkPoliciesDefaultsAsHooks{Enabled: &enabled},
		}
	})
	mustCreate(t, te.k8s, pkg)

	waitFor(t, func() error {
		var events corev1.EventList
		if err := te.k8s.List(ctx, &events, client.InNamespace("rec-hooks")); err != nil {
			return err
		}
		for _, e := range events.Items {
			if e.Reason == "UnsupportedField" && e.Type == corev1.EventTypeWarning {
				return nil
			}
		}
		return fmt.Errorf("no UnsupportedField warning event found (%d events)", len(events.Items))
	})
}

// TestReconcile_GlobalConfigDefinitions exercises the shared egress
// definition pool end to end: a Package resolves a definition from the
// global ConfigMap, editing the ConfigMap re-reconciles the Package through
// the watch, and a package-local definition shadows a shared name.
func TestReconcile_GlobalConfigDefinitions(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, globalConfigNS)
	te.ensureNamespace(t, "rec-global")
	ctx := context.Background()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: globalConfigNS, Name: globalConfigName},
		Data: map[string]string{
			"egressDefinitions": `
shared-db:
  ports:
    - port: 5432
      protocol: TCP
  to:
    - namespaceSelector: {}
      podSelector:
        matchLabels:
          app: postgres
local-wins:
  ports:
    - port: 1111
      protocol: TCP
  to:
    - ipBlock: { cidr: 10.0.0.0/8 }
`,
			"ingressDefinitions": `
shared-mon:
  from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: monitoring
      podSelector:
        matchLabels:
          app: scraper
`,
		},
	}
	mustCreate(t, te.k8s, cm)

	pkg := newPackage("example-app", "rec-global", func(p *bbv1alpha1.Package) {
		p.Spec.NetworkPolicies = &bbv1alpha1.NetworkPolicies{
			Enabled: true,
			Egress: &bbv1alpha1.NetworkPoliciesEgress{
				Definitions: map[string]bbv1alpha1.NetworkPoliciesEgressDefinitionsValue{
					"local-wins": {
						Ports: []bbv1alpha1.NetworkPoliciesEgressDefinitionsValuePortsElem{{Port: intOrStringPtr(2222), Protocol: protoPtr("TCP")}},
						To: []bbv1alpha1.NetworkPoliciesEgressDefinitionsValueToElem{{
							IPBlock: &bbv1alpha1.NetworkPoliciesEgressDefinitionsValueToElemIPBlock{CIDR: strPtr("10.0.0.0/8")},
						}},
					},
				},
				From: map[string]apiextensionsv1.JSON{
					"example-app": rawJSON(`{"to":{"definition":{"shared-db":true,"local-wins":true}}}`),
				},
			},
			Ingress: &bbv1alpha1.NetworkPoliciesIngress{
				To: map[string]apiextensionsv1.JSON{
					"example-app:8080": rawJSON(`{"from":{"definition":{"shared-mon":true}}}`),
				},
			},
		}
	})
	mustCreate(t, te.k8s, pkg)

	// Shared ingress definition resolves.
	waitFor(t, func() error {
		var np networkingv1.NetworkPolicy
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-global", Name: "allow-ingress-to-example-app-tcp-port-8080-from-shared-mon"}, &np); err != nil {
			return err
		}
		if np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app"] != "scraper" {
			return fmt.Errorf("shared-mon peer = %+v, want app=scraper", np.Spec.Ingress[0].From[0])
		}
		return nil
	})

	// Shared definition resolves: netpol on 5432 with the empty
	// namespaceSelector preserved.
	sharedNP := types.NamespacedName{Namespace: "rec-global", Name: "allow-egress-from-example-app-to-shared-db"}
	waitFor(t, func() error {
		var np networkingv1.NetworkPolicy
		if err := te.k8s.Get(ctx, sharedNP, &np); err != nil {
			return err
		}
		if got := np.Spec.Egress[0].Ports[0].Port.IntValue(); got != 5432 {
			return fmt.Errorf("shared-db port = %d, want 5432", got)
		}
		if np.Spec.Egress[0].To[0].NamespaceSelector == nil {
			return fmt.Errorf("shared-db peer lost namespaceSelector: {}")
		}
		return nil
	})

	// Local definition shadows the shared name.
	waitFor(t, func() error {
		var np networkingv1.NetworkPolicy
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-global", Name: "allow-egress-from-example-app-to-local-wins"}, &np); err != nil {
			return err
		}
		if got := np.Spec.Egress[0].Ports[0].Port.IntValue(); got != 2222 {
			return fmt.Errorf("local-wins port = %d, want 2222 (local), not the shared 1111", got)
		}
		return nil
	})

	// Editing the ConfigMap must re-reconcile the Package via the watch.
	var gotCM corev1.ConfigMap
	if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: globalConfigNS, Name: globalConfigName}, &gotCM); err != nil {
		t.Fatalf("get configmap: %v", err)
	}
	gotCM.Data["egressDefinitions"] = `
shared-db:
  ports:
    - port: 5433
      protocol: TCP
  to:
    - namespaceSelector: {}
      podSelector:
        matchLabels:
          app: postgres
local-wins:
  ports:
    - port: 1111
      protocol: TCP
  to:
    - ipBlock: { cidr: 10.0.0.0/8 }
`
	if err := te.k8s.Update(ctx, &gotCM); err != nil {
		t.Fatalf("update configmap: %v", err)
	}
	waitFor(t, func() error {
		var np networkingv1.NetworkPolicy
		if err := te.k8s.Get(ctx, sharedNP, &np); err != nil {
			return err
		}
		if got := np.Spec.Egress[0].Ports[0].Port.IntValue(); got != 5433 {
			return fmt.Errorf("shared-db port = %d, want 5433 after ConfigMap update", got)
		}
		return nil
	})
}

// TestReconcile_TwoPackagesSharedNamespace is the regression test for the
// default-AP ownership fight (#8): two Packages in one namespace, one with
// istio.prependReleaseName, must emit four distinct default
// AuthorizationPolicies and converge — no hot reconcile loop flipping
// ownership of shared-name objects.
func TestReconcile_TwoPackagesSharedNamespace(t *testing.T) {
	te := startEnv(t)
	te.ensureNamespace(t, "rec-shared")
	ctx := context.Background()

	istioOn := func(prepend bool) *bbv1alpha1.Istio {
		return &bbv1alpha1.Istio{
			Enabled:               true,
			PrependReleaseName:    prepend,
			AuthorizationPolicies: &bbv1alpha1.IstioAuthorizationPolicies{Enabled: true},
		}
	}
	plain := newPackage("monitoring", "rec-shared", func(p *bbv1alpha1.Package) {
		p.Spec.Istio = istioOn(false)
	})
	prepended := newPackage("grafana", "rec-shared", func(p *bbv1alpha1.Package) {
		p.Spec.Istio = istioOn(true)
	})
	mustCreate(t, te.k8s, plain)
	mustCreate(t, te.k8s, prepended)

	// All four default APs must exist, distinctly named.
	apNames := []string{
		"default-authz-allow-nothing",
		"default-authz-allow-all-in-ns",
		"grafana-default-authz-allow-nothing",
		"grafana-default-authz-allow-all-in-ns",
	}
	rvs := map[string]string{}
	waitFor(t, func() error {
		for _, name := range apNames {
			var ap istiosecv1.AuthorizationPolicy
			if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-shared", Name: name}, &ap); err != nil {
				return err
			}
			rvs[name] = ap.ResourceVersion
		}
		return nil
	})

	// Hot-loop detector: resourceVersions must be stable once both
	// packages converge. Before the fix, the two unprefixed APs churned
	// several times per second as each package stamped its own ownership.
	time.Sleep(3 * time.Second)
	for _, name := range apNames {
		var ap istiosecv1.AuthorizationPolicy
		if err := te.k8s.Get(ctx, types.NamespacedName{Namespace: "rec-shared", Name: name}, &ap); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if ap.ResourceVersion != rvs[name] {
			t.Errorf("%s resourceVersion churned (%s -> %s): packages are fighting over it", name, rvs[name], ap.ResourceVersion)
		}
		if len(ap.OwnerReferences) != 1 {
			t.Errorf("%s has %d ownerReferences, want exactly 1", name, len(ap.OwnerReferences))
		}
	}
}

// --- helpers ---

func intOrStringPtr(i int) *intstr.IntOrString {
	v := intstr.FromInt(i)
	return &v
}

func strPtr(s string) *string { return &s }

func protoPtr(s string) *bbv1alpha1.NetworkPoliciesEgressDefinitionsValuePortsElemProtocol {
	v := bbv1alpha1.NetworkPoliciesEgressDefinitionsValuePortsElemProtocol(s)
	return &v
}

func newPackage(name, namespace string, mutate func(*bbv1alpha1.Package)) *bbv1alpha1.Package {
	p := &bbv1alpha1.Package{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	mutate(p)
	return p
}

func mustCreate(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Create(context.Background(), obj); err != nil {
		t.Fatalf("create %T %s/%s: %v", obj, obj.GetNamespace(), obj.GetName(), err)
	}
}

func mustUpdate(t *testing.T, c client.Client, namespace, name string, mutate func(*bbv1alpha1.Package)) {
	t.Helper()
	var got bbv1alpha1.Package
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, &got); err != nil {
		t.Fatalf("get %s/%s: %v", namespace, name, err)
	}
	mutate(&got)
	if err := c.Update(context.Background(), &got); err != nil {
		t.Fatalf("update %s/%s: %v", namespace, name, err)
	}
}
