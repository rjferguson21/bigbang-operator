/*
Copyright 2026 Big Bang.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"fmt"
	"sort"

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
)

// fieldManager is the SSA owner identifier used for every applied object.
const fieldManager = "bigbang-operator"

// PackageReconciler reconciles a Package object.
type PackageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=bigbang.dev,resources=packages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bigbang.dev,resources=packages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bigbang.dev,resources=packages/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.istio.io,resources=peerauthentications;authorizationpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=sidecars;serviceentries;virtualservices,verbs=get;list;watch;create;update;patch;delete

func (r *PackageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pkg bbv1alpha1.Package
	if err := r.Get(ctx, req.NamespacedName, &pkg); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !pkg.DeletionTimestamp.IsZero() {
		// GC via owner references handles cleanup. No finalizer in v1.
		return ctrl.Result{}, nil
	}

	desired, err := generator.Generate(generator.Input{Package: &pkg, Scheme: r.Scheme})
	if err != nil {
		return r.markFailed(ctx, &pkg, "GenerationFailed", err)
	}

	if err := r.applyAll(ctx, desired); err != nil {
		return r.markFailed(ctx, &pkg, "ApplyFailed", err)
	}

	if err := r.pruneStale(ctx, &pkg, desired); err != nil {
		return r.markFailed(ctx, &pkg, "PruneFailed", err)
	}

	logger.Info("reconciled", "applied", len(desired))
	return r.markReady(ctx, &pkg, desired)
}

func (r *PackageReconciler) applyAll(ctx context.Context, desired []client.Object) error {
	for _, obj := range desired {
		if err := r.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner(fieldManager)); err != nil {
			return fmt.Errorf("apply %s/%s: %w", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
		}
	}
	return nil
}

// pruneStale lists every Kind the generator can emit, filtered by the
// package owner label, and deletes any object not in `desired`.
func (r *PackageReconciler) pruneStale(ctx context.Context, pkg *bbv1alpha1.Package, desired []client.Object) error {
	keep := make(map[string]struct{}, len(desired))
	for _, o := range desired {
		keep[objectKey(o)] = struct{}{}
	}

	for _, list := range managedListKinds() {
		if err := r.List(ctx, list,
			client.InNamespace(pkg.Namespace),
			client.MatchingLabels{generator.LabelPackage: pkg.Name},
		); err != nil {
			return fmt.Errorf("list for prune: %w", err)
		}
		items, err := extractItems(list)
		if err != nil {
			return err
		}
		for _, item := range items {
			if _, kept := keep[objectKey(item)]; kept {
				continue
			}
			if err := r.Delete(ctx, item); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete %s/%s: %w", item.GetObjectKind().GroupVersionKind().Kind, item.GetName(), err)
			}
		}
	}
	return nil
}

// managedListKinds returns one empty List per Kind the generator may emit.
// Adding a Kind to the generator REQUIRES adding it here, otherwise stale
// objects of that Kind will leak past prune.
func managedListKinds() []client.ObjectList {
	return []client.ObjectList{
		&networkingv1.NetworkPolicyList{},
		&istiosecv1.PeerAuthenticationList{},
		&istiosecv1.AuthorizationPolicyList{},
		&istionetv1.VirtualServiceList{},
		&istionetv1.ServiceEntryList{},
		&istionetv1.SidecarList{},
	}
}

func extractItems(list client.ObjectList) ([]client.Object, error) {
	switch l := list.(type) {
	case *networkingv1.NetworkPolicyList:
		out := make([]client.Object, len(l.Items))
		for i := range l.Items {
			out[i] = &l.Items[i]
		}
		return out, nil
	case *istiosecv1.PeerAuthenticationList:
		out := make([]client.Object, len(l.Items))
		for i := range l.Items {
			out[i] = l.Items[i]
		}
		return out, nil
	case *istiosecv1.AuthorizationPolicyList:
		out := make([]client.Object, len(l.Items))
		for i := range l.Items {
			out[i] = l.Items[i]
		}
		return out, nil
	case *istionetv1.VirtualServiceList:
		out := make([]client.Object, len(l.Items))
		for i := range l.Items {
			out[i] = l.Items[i]
		}
		return out, nil
	case *istionetv1.ServiceEntryList:
		out := make([]client.Object, len(l.Items))
		for i := range l.Items {
			out[i] = l.Items[i]
		}
		return out, nil
	case *istionetv1.SidecarList:
		out := make([]client.Object, len(l.Items))
		for i := range l.Items {
			out[i] = l.Items[i]
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown list type %T", list)
}

func objectKey(o client.Object) string {
	gvk := o.GetObjectKind().GroupVersionKind()
	return fmt.Sprintf("%s|%s|%s", gvk.String(), o.GetNamespace(), o.GetName())
}

func (r *PackageReconciler) markFailed(ctx context.Context, pkg *bbv1alpha1.Package, reason string, err error) (ctrl.Result, error) {
	setCondition(pkg, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: pkg.Generation,
		LastTransitionTime: metav1.Now(),
	})
	if statusErr := r.Status().Update(ctx, pkg); statusErr != nil {
		return ctrl.Result{}, fmt.Errorf("status update after %s: %v (original: %w)", reason, statusErr, err)
	}
	return ctrl.Result{}, err
}

func (r *PackageReconciler) markReady(ctx context.Context, pkg *bbv1alpha1.Package, desired []client.Object) (ctrl.Result, error) {
	setCondition(pkg, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ResourcesApplied",
		Message:            "bb-common resources reconciled",
		ObservedGeneration: pkg.Generation,
		LastTransitionTime: metav1.Now(),
	})
	pkg.Status.ObservedGeneration = pkg.Generation
	pkg.Status.AppliedResources = summarize(desired)
	if err := r.Status().Update(ctx, pkg); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func summarize(objs []client.Object) []bbv1alpha1.AppliedResource {
	out := make([]bbv1alpha1.AppliedResource, len(objs))
	for i, o := range objs {
		gvk := o.GetObjectKind().GroupVersionKind()
		out[i] = bbv1alpha1.AppliedResource{
			APIVersion: gvk.GroupVersion().String(),
			Kind:       gvk.Kind,
			Name:       o.GetName(),
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// setCondition upserts c onto pkg.Status.Conditions, replacing any condition
// with the same Type. Preserves LastTransitionTime when status didn't change.
func setCondition(pkg *bbv1alpha1.Package, c metav1.Condition) {
	for i, existing := range pkg.Status.Conditions {
		if existing.Type != c.Type {
			continue
		}
		if existing.Status == c.Status {
			c.LastTransitionTime = existing.LastTransitionTime
		}
		pkg.Status.Conditions[i] = c
		return
	}
	pkg.Status.Conditions = append(pkg.Status.Conditions, c)
}

// (unused, retained as a compile-time assertion that types.NamespacedName is
// imported — we may need it in the next reconciler revision)
var _ = types.NamespacedName{}
var _ = schema.GroupKind{}

func (r *PackageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bbv1alpha1.Package{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&istiosecv1.PeerAuthentication{}).
		Owns(&istiosecv1.AuthorizationPolicy{}).
		Owns(&istionetv1.VirtualService{}).
		Owns(&istionetv1.ServiceEntry{}).
		Owns(&istionetv1.Sidecar{}).
		Named("package").
		Complete(r)
}
