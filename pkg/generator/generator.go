// Package generator turns a Package CR into the set of Kubernetes objects to
// apply. The package is pure-Go: it does not talk to an apiserver, render
// Helm, or fetch anything from a registry. Same Input -> same []client.Object.
package generator

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// Input describes one Package to render.
type Input struct {
	Package *bbv1alpha1.Package
	// Scheme is used to set GVK on emitted objects (so SSA marshals them
	// correctly). It must contain the istio and networking types.
	Scheme *runtime.Scheme
}

// Generate returns the desired objects for in.Package. The slice is in a
// stable order: istio resources, then network policies, then routes.
func Generate(in Input) ([]client.Object, error) {
	if in.Package == nil {
		return nil, fmt.Errorf("generator: Package is nil")
	}
	if in.Scheme == nil {
		return nil, fmt.Errorf("generator: Scheme is nil")
	}

	var out []client.Object
	spec := in.Package.Spec

	if spec.Istio != nil && spec.Istio.Enabled {
		objs, err := generateIstio(in.Package, spec.Istio)
		if err != nil {
			return nil, fmt.Errorf("istio: %w", err)
		}
		out = append(out, objs...)
	}

	if spec.NetworkPolicies != nil && spec.NetworkPolicies.Enabled {
		objs, err := generateNetworkPolicies(in.Package, spec.NetworkPolicies, spec.Istio)
		if err != nil {
			return nil, fmt.Errorf("networkPolicies: %w", err)
		}
		out = append(out, objs...)
	}

	if spec.Routes != nil {
		objs, err := generateRoutes(in.Package, spec.Routes)
		if err != nil {
			return nil, fmt.Errorf("routes: %w", err)
		}
		out = append(out, objs...)
	}

	// AuthorizationPolicies live in their own pass because they depend on
	// both istio and networkPolicies state.
	out = append(out, generateDefaultAuthzPolicies(in.Package, spec.Istio, spec.NetworkPolicies)...)
	if spec.NetworkPolicies != nil {
		aps, err := generateAuthzFromIngressShorthand(in.Package, spec.NetworkPolicies, spec.Istio)
		if err != nil {
			return nil, fmt.Errorf("authorizationPolicies: %w", err)
		}
		out = append(out, aps...)
	}
	if spec.Routes != nil {
		aps, err := generateAuthzFromRoutes(in.Package, spec.Routes, spec.Istio)
		if err != nil {
			return nil, fmt.Errorf("authorizationPolicies: %w", err)
		}
		out = append(out, aps...)
	}
	customAPs, err := generateCustomAuthzPolicies(spec.Istio)
	if err != nil {
		return nil, fmt.Errorf("authorizationPolicies: %w", err)
	}
	out = append(out, customAPs...)

	// Post-process: ambient/HBONE port 15008 injection. Runs after route
	// netpols are emitted so they participate too.
	if hboneEnabled(spec.NetworkPolicies, spec.Istio) {
		injectHBonePorts(out)
	}

	for _, o := range out {
		stampMetadata(in.Package, o)
		if err := setGVK(in.Scheme, o); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func setGVK(scheme *runtime.Scheme, obj client.Object) error {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		return fmt.Errorf("gvk for %T: %w", obj, err)
	}
	if len(gvks) == 0 {
		return fmt.Errorf("no GVK for %T", obj)
	}
	obj.GetObjectKind().SetGroupVersionKind(gvks[0])
	return nil
}
