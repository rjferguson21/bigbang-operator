/*
Copyright 2026 Big Bang.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"bytes"
	"context"
	"fmt"
	"time"

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
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
	// APIReader reads straight from the apiserver (no cache) — used for the
	// one-off `default/kubernetes` Service lookup so we don't start a
	// cluster-wide Service informer. Falls back to Client when nil.
	APIReader client.Reader
	// Recorder emits Events for spec fields the generator accepts but does
	// not honor. Optional; nil disables events.
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=bigbang.dev,resources=packages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bigbang.dev,resources=packages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bigbang.dev,resources=packages/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.istio.io,resources=peerauthentications;authorizationpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=sidecars;serviceentries;virtualservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *PackageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pkg bbv1alpha1.Package
	if err := r.Get(ctx, req.NamespacedName, &pkg); err != nil {
		if apierrors.IsNotFound(err) {
			// Object is gone; controller-runtime will fire one more reconcile
			// for the delete event but the metric labels would be stale, so
			// don't emit anything here.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !pkg.DeletionTimestamp.IsZero() {
		// GC via owner references handles cleanup. No finalizer in v1.
		return ctrl.Result{}, nil
	}

	if pkg.Generation != pkg.Status.ObservedGeneration {
		if err := r.markReconciling(ctx, &pkg); err != nil {
			return ctrl.Result{}, err
		}
	}

	start := time.Now()
	defer func() {
		reconcileDurationSeconds.WithLabelValues(pkg.Namespace, pkg.Name).Observe(time.Since(start).Seconds())
	}()

	if r.Recorder != nil {
		for _, w := range generator.Warnings(&pkg) {
			r.Recorder.Event(&pkg, corev1.EventTypeWarning, "UnsupportedField", w)
		}
	}

	desired, err := generator.Generate(generator.Input{
		Package:      &pkg,
		Scheme:       r.Scheme,
		KubeAPIPorts: r.kubeAPIPorts(ctx, &pkg),
	})
	if err != nil {
		reconcileTotal.WithLabelValues(pkg.Namespace, pkg.Name, outcomeGenerationFailed).Inc()
		return r.markFailed(ctx, &pkg, "GenerationFailed", err)
	}

	if err := r.applyAll(ctx, desired); err != nil {
		reconcileTotal.WithLabelValues(pkg.Namespace, pkg.Name, outcomeApplyFailed).Inc()
		return r.markFailed(ctx, &pkg, "ApplyFailed", err)
	}

	if err := r.pruneStale(ctx, &pkg, desired); err != nil {
		reconcileTotal.WithLabelValues(pkg.Namespace, pkg.Name, outcomePruneFailed).Inc()
		return r.markFailed(ctx, &pkg, "PruneFailed", err)
	}

	reconcileTotal.WithLabelValues(pkg.Namespace, pkg.Name, outcomeSuccess).Inc()
	appliedResources.WithLabelValues(pkg.Namespace, pkg.Name).Set(float64(len(desired)))

	logger.Info("reconciled", "applied", len(desired))
	return r.markReady(ctx, &pkg, desired)
}

// kubeAPIPorts resolves the API server's target ports from the
// `default/kubernetes` Service, mirroring bb-common's render-time lookup
// for the built-in `kubeAPI` egress definition. Skipped entirely unless the
// spec references that definition; on lookup failure the definition falls
// back to all-ports (bb-common behaves the same when its lookup is empty).
func (r *PackageReconciler) kubeAPIPorts(ctx context.Context, pkg *bbv1alpha1.Package) []intstr.IntOrString {
	np := pkg.Spec.NetworkPolicies
	if np == nil || !np.Enabled || np.Egress == nil {
		return nil
	}
	referenced := false
	for _, raw := range np.Egress.From {
		if bytes.Contains(raw.Raw, []byte(`"kubeAPI"`)) {
			referenced = true
			break
		}
	}
	if !referenced {
		return nil
	}

	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var svc corev1.Service
	if err := reader.Get(ctx, types.NamespacedName{Namespace: "default", Name: "kubernetes"}, &svc); err != nil {
		log.FromContext(ctx).V(1).Info("kubeAPI definition: Service default/kubernetes lookup failed, emitting without port restriction", "error", err)
		return nil
	}
	ports := make([]intstr.IntOrString, 0, len(svc.Spec.Ports))
	for _, p := range svc.Spec.Ports {
		ports = append(ports, p.TargetPort)
	}
	return ports
}

func (r *PackageReconciler) applyAll(ctx context.Context, desired []client.Object) error {
	for _, obj := range desired {
		if err := r.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner(fieldManager)); err != nil {
			return fmt.Errorf("apply %s/%s: %w", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
		}
	}
	return nil
}

// (unused, retained as a compile-time assertion that types.NamespacedName is
// imported — we may need it in the next reconciler revision)
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
