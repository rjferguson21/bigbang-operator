package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	bb "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/helmhooks"
)

func hookLifetime(obj client.Object) (time.Duration, error) {
	seconds := helmhooks.DefaultTimeoutSeconds
	if raw := obj.GetAnnotations()[helmhooks.Timeout]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 86400 {
			return 0, fmt.Errorf("invalid hook timeout %q", raw)
		}
		seconds = n
	}
	return time.Until(obj.GetCreationTimestamp().Add(time.Duration(seconds) * time.Second)), nil
}

// HookPolicyReconciler only expires Helm-rendered policies. It never generates
// or adopts them, so Package pruning and Helm hook replacement stay independent.
type HookPolicyReconciler struct{ client.Client }

func (r *HookPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var policy networkingv1.NetworkPolicy
	if err := r.Get(ctx, req.NamespacedName, &policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if policy.Labels[helmhooks.Release] == "" || policy.Annotations[helmhooks.Token] == "" ||
		policy.Labels["bigbang.dev/package"] != "" || len(policy.OwnerReferences) != 0 {
		return ctrl.Result{}, nil
	}
	remaining, err := hookLifetime(&policy)
	if err != nil {
		return ctrl.Result{}, err
	}
	if remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}
	return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, &policy, client.Preconditions{
		UID: &policy.UID, ResourceVersion: &policy.ResourceVersion,
	}))
}

func (r *HookPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("helm-hook-policy").
		For(&networkingv1.NetworkPolicy{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetLabels()[helmhooks.Release] != "" && obj.GetAnnotations()[helmhooks.Token] != ""
		})).Complete(r)
}

func (r *PackageReconciler) cleanupHookPolicies(ctx context.Context, marker *bb.Package) (ctrl.Result, error) {
	release, token := marker.Annotations[helmhooks.Release], marker.Annotations[helmhooks.Token]
	if release == "" || token == "" {
		return ctrl.Result{}, fmt.Errorf("hook cleanup requires release and token")
	}
	phase := marker.Annotations[helmhooks.Phase]
	switch phase {
	case "install", "upgrade", "rollback", "delete":
	default:
		return ctrl.Result{}, fmt.Errorf("invalid cleanup phase %q", phase)
	}
	remaining, err := hookLifetime(marker)
	if err != nil {
		return ctrl.Result{}, err
	}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	if phase != "delete" && remaining > 0 {
		var pkg bb.Package
		err := reader.Get(ctx, types.NamespacedName{Namespace: marker.Namespace, Name: release}, &pkg)
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		if err != nil {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		// Keep the bridge policies until the regular Package has applied its rules.
		// A newer operation has its own token and can safely supersede this marker.
		if pkg.Annotations[helmhooks.Token] == token &&
			(pkg.Status.ObservedGeneration != pkg.Generation || !meta.IsStatusConditionTrue(pkg.Status.Conditions, "Ready")) {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}
	var policies networkingv1.NetworkPolicyList
	if err := reader.List(ctx, &policies, client.InNamespace(marker.Namespace), client.MatchingLabels{helmhooks.Release: release}); err != nil {
		return ctrl.Result{}, err
	}
	for i := range policies.Items {
		policy := &policies.Items[i]
		if policy.Annotations[helmhooks.Token] != token || !policy.CreationTimestamp.Before(&marker.CreationTimestamp) ||
			len(policy.OwnerReferences) != 0 || policy.Labels["bigbang.dev/package"] != "" {
			continue
		}
		if err := r.Delete(ctx, policy, client.Preconditions{UID: &policy.UID, ResourceVersion: &policy.ResourceVersion}); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, marker, client.Preconditions{
		UID: &marker.UID, ResourceVersion: &marker.ResourceVersion,
	}))
}
