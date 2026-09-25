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

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
)

// Prune machinery: list every Kind the generator can emit, filter by the
// package owner label, and delete what the latest reconcile no longer wants.

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
