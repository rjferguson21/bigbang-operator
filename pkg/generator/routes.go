package generator

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// generateRoutes renders Istio routing resources for inbound/outbound entries.
//
// v1 scope: stubbed. Inbound (VirtualService/ServiceEntry/NetworkPolicy/
// AuthorizationPolicy) and outbound (ServiceEntry) generation will land
// after the shorthand-decoding work that unblocks `routes.inbound` and
// `routes.outbound` (today they're typed as map[string]apiextensionsv1.JSON
// because their schema uses patternProperties — see
// hack/schema-to-go.sh and the operator notes in plan/routes.md).
func generateRoutes(pkg *bbv1alpha1.Package, spec *bbv1alpha1.Routes) ([]client.Object, error) {
	return nil, nil
}
