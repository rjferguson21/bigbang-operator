package generator

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

const (
	GlobalConfigRoutesKey   = "routes"
	GlobalConfigGatewaysKey = "gateways"
)

// IngressConfig is the shared ingress configuration in the global ConfigMap.
type IngressConfig struct {
	Routes   SharedRoutes                 `json:"routes,omitempty"`
	Gateways map[string]GatewayDefinition `json:"gateways,omitempty"`
}

// SharedRoutes exposes only the supported inbound defaults.
type SharedRoutes struct {
	Defaults *bbv1alpha1.RouteDefaults `json:"defaults,omitempty"`
}

// GatewayDefinition describes gateway pods in the gateway reference's namespace.
type GatewayDefinition struct {
	PodSelector        *metav1.LabelSelector `json:"podSelector"`
	ServiceAccountName string                `json:"serviceAccountName"`
}

// ParseIngressConfig rejects partial definitions instead of guessing identities.
func ParseIngressConfig(routes, gateways string) (*IngressConfig, error) {
	cfg := &IngressConfig{}
	if err := yaml.UnmarshalStrict([]byte(routes), &cfg.Routes); err != nil {
		return nil, fmt.Errorf("routes: %w", err)
	}
	if err := yaml.UnmarshalStrict([]byte(gateways), &cfg.Gateways); err != nil {
		return nil, fmt.Errorf("gateways: %w", err)
	}
	for _, ref := range sortedKeys(cfg.Gateways) {
		def := cfg.Gateways[ref]
		if _, _, ok := splitGateway(ref); !ok {
			return nil, fmt.Errorf("gateways.%s: reference must be <namespace>/<name>", ref)
		}
		if def.PodSelector == nil {
			return nil, fmt.Errorf("gateways.%s: podSelector is required", ref)
		}
		if _, err := metav1.LabelSelectorAsSelector(def.PodSelector); err != nil {
			return nil, fmt.Errorf("gateways.%s.podSelector: %w", ref, err)
		}
		if errs := validation.IsDNS1123Subdomain(def.ServiceAccountName); len(errs) != 0 {
			return nil, fmt.Errorf("gateways.%s.serviceAccountName: %s", ref, strings.Join(errs, ", "))
		}
	}
	return cfg, nil
}

type resolvedGateway struct {
	namespace, name string
	definition      GatewayDefinition
}

type resolvedInbound struct {
	name     string
	route    *bbv1alpha1.InboundRoute
	gateways []resolvedGateway
}

func defaultGateways(defaults *bbv1alpha1.RouteDefaults) []string {
	if defaults == nil || defaults.Inbound == nil {
		return nil
	}
	return defaults.Inbound.Gateways
}

func resolveInboundRoutes(in Input) ([]resolvedInbound, error) {
	spec := in.Package.Spec.Routes
	if spec == nil {
		return nil, nil
	}
	var out []resolvedInbound
	for _, name := range sortedKeys(spec.Inbound) {
		route, err := decodeInbound(spec.Inbound[name])
		if err != nil {
			return nil, fmt.Errorf("routes.inbound.%s: %w", name, err)
		}
		if !route.Enabled {
			continue
		}
		if in.SharedIngressConfigError != nil {
			return nil, fmt.Errorf("routes.inbound.%s: global config unreadable: %w", name, in.SharedIngressConfigError)
		}
		if route.Gateways == nil {
			route.Gateways = defaultGateways(spec.Defaults)
			if route.Gateways == nil && in.SharedIngressConfig != nil {
				route.Gateways = defaultGateways(in.SharedIngressConfig.Routes.Defaults)
			}
		}
		if len(route.Selector) == 0 {
			route.Selector = map[string]string{"app.kubernetes.io/name": name}
		}
		if err := validateInbound(name, route); err != nil {
			return nil, err
		}
		resolved := resolvedInbound{name: name, route: route}
		for _, ref := range route.Gateways {
			ns, gw, _ := splitGateway(ref)
			def := GatewayDefinition{
				PodSelector:        &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": gw}},
				ServiceAccountName: gw + "-ingressgateway-service-account",
			}
			if in.SharedIngressConfig != nil {
				if configured, ok := in.SharedIngressConfig.Gateways[ref]; ok {
					def = configured
				}
			}
			resolved.gateways = append(resolved.gateways, resolvedGateway{namespace: ns, name: gw, definition: def})
		}
		out = append(out, resolved)
	}
	return out, nil
}
