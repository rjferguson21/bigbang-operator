package controller

import (
	"context"
	"testing"
	"time"

	bb "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/helmhooks"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func hookPolicy(name, release, token string, age time.Duration) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "shared", UID: types.UID(name), CreationTimestamp: metav1.NewTime(time.Now().Add(-age)),
		Labels:      map[string]string{helmhooks.Release: release},
		Annotations: map[string]string{helmhooks.Token: token, helmhooks.Timeout: "60"},
	}}
}

func hookClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := bb.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestHookPolicyExpiration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		age     time.Duration
		managed bool
		deleted bool
	}{
		{"active", time.Second, false, false},
		{"expired", 2 * time.Minute, false, true},
		{"persistent", 2 * time.Minute, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := hookPolicy(tc.name, "monitoring", "one", tc.age)
			if tc.managed {
				policy.Labels["bigbang.dev/package"] = "monitoring"
			}
			c := hookClient(t, policy)
			result, err := (&HookPolicyReconciler{Client: c}).Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(policy)})
			if err != nil {
				t.Fatal(err)
			}
			err = c.Get(context.Background(), client.ObjectKeyFromObject(policy), &networkingv1.NetworkPolicy{})
			if (err != nil) != tc.deleted {
				t.Fatalf("deleted=%t, get=%v", tc.deleted, err)
			}
			if tc.name == "active" && result.RequeueAfter <= 0 {
				t.Fatal("active policy has no expiration scheduled")
			}
		})
	}
}

func TestHookCleanupOwnershipAndHandoff(t *testing.T) {
	ctx := context.Background()
	marker := &bb.Package{ObjectMeta: metav1.ObjectMeta{Name: "cleanup", Namespace: "shared", UID: "marker", CreationTimestamp: metav1.NewTime(time.Now().Truncate(time.Second)),
		Annotations: map[string]string{helmhooks.Action: "cleanup", helmhooks.Release: "monitoring", helmhooks.Token: "one", helmhooks.Phase: "upgrade"}}}
	pkg := &bb.Package{ObjectMeta: metav1.ObjectMeta{Name: "monitoring", Namespace: "shared", Generation: 2, Annotations: map[string]string{helmhooks.Token: "one"}}, Status: bb.PackageStatus{ObservedGeneration: 1}}
	owned := hookPolicy("owned", "monitoring", "one", time.Minute)
	other := hookPolicy("other-release", "another", "one", time.Minute)
	newer := hookPolicy("newer-token", "monitoring", "two", time.Minute)
	later := hookPolicy("newer-operation", "monitoring", "one", -time.Minute)
	sameSecond := hookPolicy("same-second", "monitoring", "one", 0)
	sameSecond.CreationTimestamp = marker.CreationTimestamp
	persistent := hookPolicy("persistent", "monitoring", "one", time.Minute)
	persistent.OwnerReferences = []metav1.OwnerReference{{APIVersion: bb.GroupVersion.String(), Kind: "Package", Name: pkg.Name, UID: "persistent-owner"}}
	c := hookClient(t, marker, pkg, owned, other, newer, later, sameSecond, persistent)
	r := &PackageReconciler{Client: c, APIReader: c}
	result, err := r.cleanupHookPolicies(ctx, marker)
	if err != nil || result.RequeueAfter <= 0 {
		t.Fatalf("expected handoff wait, got %v %v", result, err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(owned), &networkingv1.NetworkPolicy{}); err != nil {
		t.Fatal("deleted before persistent policies were ready")
	}
	pkg.Status.ObservedGeneration = 2
	pkg.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
	if err := c.Update(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cleanupHookPolicies(ctx, marker); err != nil {
		t.Fatal(err)
	}
	for _, object := range []client.Object{owned, marker} {
		if err := c.Get(ctx, client.ObjectKeyFromObject(object), object); err == nil {
			t.Fatalf("%s not cleaned", object.GetName())
		}
	}
	for _, policy := range []*networkingv1.NetworkPolicy{other, newer, later, sameSecond, persistent} {
		if err := c.Get(ctx, client.ObjectKeyFromObject(policy), policy); err != nil {
			t.Fatalf("removed unrelated policy %s: %v", policy.Name, err)
		}
	}
}

func TestHookCleanupUninstallWithoutPackage(t *testing.T) {
	marker := &bb.Package{ObjectMeta: metav1.ObjectMeta{Name: "cleanup", Namespace: "shared", UID: "marker", CreationTimestamp: metav1.NewTime(time.Now().Truncate(time.Second)), Annotations: map[string]string{
		helmhooks.Release: "monitoring", helmhooks.Token: "one", helmhooks.Phase: "delete",
	}}}
	policy := hookPolicy("hook", "monitoring", "one", time.Minute)
	c := hookClient(t, marker, policy)
	if _, err := (&PackageReconciler{Client: c}).cleanupHookPolicies(context.Background(), marker); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(policy), policy); err == nil {
		t.Fatal("uninstall left hook policy")
	}
}
