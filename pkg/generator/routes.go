package generator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	istionetv1alpha3 "istio.io/api/networking/v1alpha3"
	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// generateRoutes renders the Istio routing resources for the package.
//
// Per inbound route the operator emits a VirtualService, a paired
// ServiceEntry, and (when networkPolicies.enabled) a gateway-permitting
// NetworkPolicy. Per outbound route it emits a single ServiceEntry.
// Per-route AuthorizationPolicies are emitted by generateAuthzFromRoutes,
// gated on istio.authorizationPolicies.enabled.
func generateRoutes(pkg *bbv1alpha1.Package, spec *bbv1alpha1.Routes) ([]client.Object, error) {
	if spec == nil {
		return nil, nil
	}
	netpolsEnabled := pkg.Spec.NetworkPolicies != nil && pkg.Spec.NetworkPolicies.Enabled

	var out []client.Object
	prepend := spec.PrependReleaseName

	for _, name := range sortedKeys(spec.Inbound) {
		raw := spec.Inbound[name]
		route, err := decodeInbound(raw)
		if err != nil {
			return nil, fmt.Errorf("routes.inbound.%s: %w", name, err)
		}
		if !route.Enabled {
			continue
		}
		// Mirror bb-common's selector inference: when the user omits
		// `selector`, default to `app.kubernetes.io/name=<route-name>`.
		// Lets minimal route specs work without restating the obvious.
		if len(route.Selector) == 0 {
			route.Selector = map[string]string{"app.kubernetes.io/name": name}
		}
		if err := validateInbound(name, route); err != nil {
			return nil, err
		}
		vs, err := buildVirtualService(pkg, prepend, name, route)
		if err != nil {
			return nil, err
		}
		out = append(out, vs)
		out = append(out, buildInboundServiceEntry(pkg, prepend, name, route))
		if netpolsEnabled {
			out = append(out, buildInboundNetpols(pkg, prepend, name, route)...)
		}
	}

	for _, name := range sortedKeys(spec.Outbound) {
		raw := spec.Outbound[name]
		route, err := decodeOutbound(raw)
		if err != nil {
			return nil, fmt.Errorf("routes.outbound.%s: %w", name, err)
		}
		if !route.Enabled {
			continue
		}
		if err := validateOutbound(name, route); err != nil {
			return nil, err
		}
		out = append(out, buildOutboundServiceEntry(pkg, prepend, name, route))
	}

	return out, nil
}

// validateInbound checks fields the CRD schema can't validate
// (routes.inbound is x-kubernetes-preserve-unknown-fields, so apiserver
// validation is bypassed). Errors here surface as the Package's Ready=False
// reason "GenerationFailed".
func validateInbound(name string, r *bbv1alpha1.InboundRoute) error {
	if len(r.Gateways) == 0 {
		return fmt.Errorf("routes.inbound.%s: gateways[] must be non-empty when enabled", name)
	}
	for _, gw := range r.Gateways {
		if _, _, ok := splitGateway(gw); !ok {
			return fmt.Errorf("routes.inbound.%s: gateway %q must be <namespace>/<name>", name, gw)
		}
	}
	if r.Service == "" {
		return fmt.Errorf("routes.inbound.%s: service is required when enabled", name)
	}
	if r.Port == nil {
		return fmt.Errorf("routes.inbound.%s: port is required when enabled", name)
	}
	return nil
}

func validateOutbound(name string, r *bbv1alpha1.OutboundRoute) error {
	if len(r.Hosts) == 0 {
		return fmt.Errorf("routes.outbound.%s: hosts[] must be non-empty when enabled", name)
	}
	return nil
}

func decodeInbound(j any) (*bbv1alpha1.InboundRoute, error) {
	b, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	var r bbv1alpha1.InboundRoute
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func decodeOutbound(j any) (*bbv1alpha1.OutboundRoute, error) {
	b, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	var r bbv1alpha1.OutboundRoute
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// sortedKeys lets the generator emit resources in deterministic order so
// goldens compare cleanly across runs.
func sortedKeys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func buildVirtualService(pkg *bbv1alpha1.Package, prepend bool, name string, r *bbv1alpha1.InboundRoute) (client.Object, error) {
	vs := &istionetv1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:        prependName(prepend, pkg.Name, name),
			Labels:      mergeMaps(r.Labels, nil),
			Annotations: mergeMaps(r.Annotations, nil),
		},
		Spec: istionetv1alpha3.VirtualService{
			Hosts:    r.Hosts,
			Gateways: r.Gateways,
		},
	}

	// passthrough.enabled mutually excludes spec.http (matches bb-common).
	if r.Passthrough != nil && r.Passthrough.Enabled {
		gwPort := uint32(8443)
		if r.Passthrough.GatewayPort != nil {
			gwPort = uint32(*r.Passthrough.GatewayPort)
		}
		dest := &istionetv1alpha3.Destination{Host: r.Service}
		if r.Port != nil {
			dest.Port = &istionetv1alpha3.PortSelector{Number: portNumber(r.Port)}
		}
		vs.Spec.Tls = []*istionetv1alpha3.TLSRoute{{
			Match: []*istionetv1alpha3.TLSMatchAttributes{{
				Port:     gwPort,
				SniHosts: r.Hosts,
			}},
			Route: []*istionetv1alpha3.RouteDestination{{Destination: dest}},
		}}
		return vs, nil
	}

	httpRoutes, err := buildHTTPRoutes(r)
	if err != nil {
		return nil, fmt.Errorf("routes.inbound.%s.http: %w", name, err)
	}
	vs.Spec.Http = httpRoutes
	return vs, nil
}

// buildHTTPRoutes returns the spec.http slice. When the user supplies
// `route.http`, each raw HTTPRoute entry is JSON-round-tripped into the
// istio proto type (so SSA marshals it correctly). Otherwise the operator
// emits the single-destination default that mirrors bb-common.
func buildHTTPRoutes(r *bbv1alpha1.InboundRoute) ([]*istionetv1alpha3.HTTPRoute, error) {
	if len(r.Http) > 0 {
		out := make([]*istionetv1alpha3.HTTPRoute, 0, len(r.Http))
		for i, raw := range r.Http {
			hr := &istionetv1alpha3.HTTPRoute{}
			if err := json.Unmarshal(raw.Raw, hr); err != nil {
				return nil, fmt.Errorf("http[%d]: %w", i, err)
			}
			out = append(out, hr)
		}
		return out, nil
	}
	dest := &istionetv1alpha3.Destination{Host: r.Service}
	if r.ContainerPort != nil {
		dest.Port = &istionetv1alpha3.PortSelector{Number: portNumber(r.ContainerPort)}
	} else if r.Port != nil {
		dest.Port = &istionetv1alpha3.PortSelector{Number: portNumber(r.Port)}
	}
	return []*istionetv1alpha3.HTTPRoute{{
		Route: []*istionetv1alpha3.HTTPRouteDestination{{Destination: dest}},
	}}, nil
}

func buildInboundServiceEntry(pkg *bbv1alpha1.Package, prepend bool, name string, r *bbv1alpha1.InboundRoute) client.Object {
	port := portNumber(r.Port)
	if r.ContainerPort != nil {
		port = portNumber(r.ContainerPort)
	}
	return &istionetv1.ServiceEntry{
		ObjectMeta: metav1.ObjectMeta{
			Name:        prependName(prepend, pkg.Name, name+"-internal"),
			Labels:      mergeMaps(r.Labels, nil),
			Annotations: mergeMaps(r.Annotations, nil),
		},
		Spec: istionetv1alpha3.ServiceEntry{
			Hosts:      r.Hosts,
			Location:   istionetv1alpha3.ServiceEntry_MESH_INTERNAL,
			Resolution: parseResolution(r.Resolution),
			Ports: []*istionetv1alpha3.ServicePort{{
				Number:   port,
				Name:     "http",
				Protocol: "HTTP",
			}},
		},
	}
}

func buildInboundNetpols(pkg *bbv1alpha1.Package, prepend bool, _ string, r *bbv1alpha1.InboundRoute) []client.Object {
	out := make([]client.Object, 0, len(r.Gateways))
	for _, gw := range r.Gateways {
		gwNS, gwName, ok := splitGateway(gw)
		if !ok {
			continue
		}
		port := intstr.FromInt(int(portNumber(r.Port)))
		tcp := corev1.ProtocolTCP
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name: prependName(prepend, pkg.Name, fmt.Sprintf("allow-ingress-to-%s-%d-from-ns-%s-pod-%s",
					serviceLeaf(r.Service), portNumber(r.Port), gwNS, gwName)),
				Labels:      mergeMaps(r.Labels, map[string]string{LabelNetpolSource: LabelNetpolSourceValue, "network-policies.bigbang.dev/direction": "ingress"}),
				Annotations: mergeMaps(r.Annotations, nil),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: r.Selector},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"kubernetes.io/metadata.name": gwNS},
						},
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app.kubernetes.io/name": gwName},
						},
					}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
				}},
			},
		})
	}
	return out
}

func buildOutboundServiceEntry(pkg *bbv1alpha1.Package, prepend bool, name string, r *bbv1alpha1.OutboundRoute) client.Object {
	ports := r.Ports
	if len(ports) == 0 {
		// bb-common's default — note Protocol is HTTPS (the L7 name), not
		// TLS. Istio accepts both but HTTPS is what bb-common ships.
		ports = []bbv1alpha1.OutboundRoutePort{{Number: 443, Name: "https", Protocol: "HTTPS"}}
	}
	sePorts := make([]*istionetv1alpha3.ServicePort, 0, len(ports))
	for _, p := range ports {
		sePorts = append(sePorts, &istionetv1alpha3.ServicePort{
			Number:   p.Number,
			Name:     p.Name,
			Protocol: p.Protocol,
		})
	}

	var labels, annotations map[string]string
	if r.Metadata != nil {
		labels = r.Metadata.Labels
		annotations = r.Metadata.Annotations
	}

	loc := parseLocation(r.Location)
	// Match bb-common's suffix-by-location naming.
	suffix := "-external"
	if loc == istionetv1alpha3.ServiceEntry_MESH_INTERNAL {
		suffix = "-internal"
	}

	return &istionetv1.ServiceEntry{
		ObjectMeta: metav1.ObjectMeta{
			Name:        prependName(prepend, pkg.Name, name+suffix),
			Labels:      mergeMaps(labels, nil),
			Annotations: mergeMaps(annotations, nil),
		},
		Spec: istionetv1alpha3.ServiceEntry{
			Hosts:      r.Hosts,
			Location:   loc,
			Resolution: parseResolution(r.Resolution),
			Ports:      sePorts,
		},
	}
}

func parseLocation(s string) istionetv1alpha3.ServiceEntry_Location {
	switch strings.ToUpper(s) {
	case "MESH_INTERNAL":
		return istionetv1alpha3.ServiceEntry_MESH_INTERNAL
	default:
		return istionetv1alpha3.ServiceEntry_MESH_EXTERNAL
	}
}

func parseResolution(s string) istionetv1alpha3.ServiceEntry_Resolution {
	switch strings.ToUpper(s) {
	case "STATIC":
		return istionetv1alpha3.ServiceEntry_STATIC
	case "DNS_ROUND_ROBIN":
		return istionetv1alpha3.ServiceEntry_DNS_ROUND_ROBIN
	case "NONE":
		return istionetv1alpha3.ServiceEntry_NONE
	default:
		return istionetv1alpha3.ServiceEntry_DNS
	}
}

func portNumber(p *intstr.IntOrString) uint32 {
	if p == nil {
		return 0
	}
	if p.Type == intstr.Int {
		return uint32(p.IntValue())
	}
	// Named ports aren't valid on ServiceEntry/Destination — caller is
	// expected to supply numeric. Returning 0 surfaces the problem
	// loudly rather than guessing.
	return 0
}

func splitGateway(s string) (ns, name string, ok bool) {
	i := strings.Index(s, "/")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// serviceLeaf strips the FQDN tail from a service value so it works in
// netpol names: "logging-loki-gateway.logging.svc.cluster.local" -> "logging-loki-gateway".
func serviceLeaf(s string) string {
	if i := strings.Index(s, "."); i > 0 {
		return s[:i]
	}
	return s
}

func mergeMaps(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
