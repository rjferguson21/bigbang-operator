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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// Status machinery: condition transitions and the appliedResources summary
// surfaced on the Package CR. Follows the kstatus conventions: Ready for
// humans, plus abnormal-true Reconciling/Stalled conditions and
// observedGeneration so kstatus-based tooling (cli-utils, Flux health
// checks, ArgoCD) can compute InProgress/Failed/Current.

// markReconciling surfaces Reconciling=True before working on a not-yet-
// observed generation, so watchers see InProgress instead of a stale
// Current/Failed while the reconcile runs.
func (r *PackageReconciler) markReconciling(ctx context.Context, pkg *bbv1alpha1.Package) error {
	setCondition(pkg, metav1.Condition{
		Type:               "Reconciling",
		Status:             metav1.ConditionTrue,
		Reason:             "Progressing",
		Message:            fmt.Sprintf("reconciling generation %d", pkg.Generation),
		ObservedGeneration: pkg.Generation,
		LastTransitionTime: metav1.Now(),
	})
	return r.Status().Update(ctx, pkg)
}

// markFailed returns ctrl.Result{} unconditionally so it can be used as a
// direct tail-call from Reconcile. The empty Result is required by the
// controller-runtime signature; lint sees it as always-nil but it is part
// of the contract.
//
//nolint:unparam
func (r *PackageReconciler) markFailed(ctx context.Context, pkg *bbv1alpha1.Package, reason string, err error) (ctrl.Result, error) {
	setCondition(pkg, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: pkg.Generation,
		LastTransitionTime: metav1.Now(),
	})
	setCondition(pkg, metav1.Condition{
		Type:               "Stalled",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: pkg.Generation,
		LastTransitionTime: metav1.Now(),
	})
	removeCondition(pkg, "Reconciling")
	// Stamp observedGeneration even on failure: it means "this generation
	// was processed", not "applied successfully". Without it, kstatus reports
	// InProgress forever instead of Failed via Stalled=True.
	pkg.Status.ObservedGeneration = pkg.Generation
	if statusErr := r.Status().Update(ctx, pkg); statusErr != nil {
		return ctrl.Result{}, fmt.Errorf("status update after %s: %v (original: %w)", reason, statusErr, err)
	}
	return ctrl.Result{}, err
}

//nolint:unparam // ctrl.Result is required by the controller-runtime contract.
func (r *PackageReconciler) markReady(ctx context.Context, pkg *bbv1alpha1.Package, desired []client.Object) (ctrl.Result, error) {
	setCondition(pkg, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ResourcesApplied",
		Message:            "bb-common resources reconciled",
		ObservedGeneration: pkg.Generation,
		LastTransitionTime: metav1.Now(),
	})
	removeCondition(pkg, "Stalled")
	removeCondition(pkg, "Reconciling")
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

// removeCondition drops the condition with the given type, if present.
// Reconciling and Stalled are abnormal-true: absent means not applicable.
func removeCondition(pkg *bbv1alpha1.Package, condType string) {
	for i, existing := range pkg.Status.Conditions {
		if existing.Type == condType {
			pkg.Status.Conditions = append(pkg.Status.Conditions[:i], pkg.Status.Conditions[i+1:]...)
			return
		}
	}
}
