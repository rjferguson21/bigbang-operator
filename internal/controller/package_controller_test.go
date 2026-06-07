/*
Copyright 2026 Big Bang.
*/

package controller_test

import (
	"context"
	"fmt"
	"testing"

	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
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
		for _, c := range got.Status.Conditions {
			if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
				return nil
			}
		}
		return fmt.Errorf("Ready=True condition not found")
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

// --- helpers ---

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
