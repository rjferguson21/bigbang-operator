// Package generator turns a Package CR into the set of Kubernetes objects to
// apply. The package is pure-Go: it does not talk to an apiserver, render
// Helm, or fetch anything from a registry. Same Input -> same []client.Object.
package generator

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// Input describes one Package to render.
type Input struct {
	Package *bbv1alpha1.Package
	// Scheme is used to set GVK on emitted objects (so SSA marshals them
	// correctly). It must contain the istio and networking types.
	Scheme *runtime.Scheme
	// KubeAPIPorts restricts the built-in `kubeAPI` egress definition to
	// the API server's target ports. The controller fills it from the
	// `default/kubernetes` Service (mirroring bb-common's render-time
	// lookup); empty means no port restriction.
	KubeAPIPorts []intstr.IntOrString
	// SharedEgressDefinitions / SharedIngressDefinitions are the
	// cluster-wide definition pools from the operator's global ConfigMap,
	// referencable by any Package exactly like package-local definitions.
	// Precedence on name collision: built-in < shared < package-local.
	SharedEgressDefinitions  map[string]bbv1alpha1.NetworkPoliciesEgressDefinitionsValue
	SharedIngressDefinitions map[string]bbv1alpha1.NetworkPoliciesIngressDefinitionsValue
	// SharedDefinitionsError is set when the global ConfigMap exists but
	// cannot be parsed. Resolving any definition not declared
	// package-locally then fails with this error: the shared pools could
	// have held or overridden the name, so falling back silently would be
	// unsafe.
	SharedDefinitionsError error
}

// Warnings returns human-readable notes about spec fields the generator
// accepts but deliberately does not honor. The controller surfaces them as
// Events on the Package so a migrated bb-common values file never
// silently loses behavior.
func Warnings(pkg *bbv1alpha1.Package) []string {
	var out []string
	np := pkg.Spec.NetworkPolicies
	if np != nil && np.DefaultsAsHooks != nil && np.DefaultsAsHooks.Enabled != nil && *np.DefaultsAsHooks.Enabled {
		out = append(out, "networkPolicies.defaultsAsHooks is ignored: Helm hooks have no operator equivalent (default policies are applied and kept in sync continuously)")
	}
	return out
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
		env := defsEnv{
			kubeAPIPorts:  in.KubeAPIPorts,
			sharedEgress:  in.SharedEgressDefinitions,
			sharedIngress: in.SharedIngressDefinitions,
			sharedErr:     in.SharedDefinitionsError,
		}
		objs, err := generateNetworkPolicies(in.Package, spec.NetworkPolicies, spec.Istio, env)
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
