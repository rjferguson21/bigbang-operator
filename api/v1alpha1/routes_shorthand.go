package v1alpha1

import "k8s.io/apimachinery/pkg/util/intstr"

// InboundRoute is the typed view of one entry under `routes.inbound.<name>`.
// The generator decodes the RoutesInbound map values (apiextensionsv1.JSON,
// because the schema uses patternProperties) into this struct at render
// time. Fields mirror plan/routes.md and bb-common's
// templates/routes/inbound.
type InboundRoute struct {
	// Enabled lets a route be retained in spec but skipped from rendering.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Gateways are `<namespace>/<gateway-name>` strings, attached to the
	// generated VirtualService.
	Gateways []string `json:"gateways,omitempty"`

	// Hosts are the VirtualService hostnames.
	Hosts []string `json:"hosts,omitempty"`

	// Service is the in-cluster destination (Service name or FQDN).
	Service string `json:"service,omitempty"`

	// Port is the destination port — int or string (named port).
	Port *intstr.IntOrString `json:"port,omitempty"`

	// ContainerPort overrides the upstream container port; defaults to Port.
	// +optional
	ContainerPort *intstr.IntOrString `json:"containerPort,omitempty"`

	// Selector is the pod label selector for the generated NetworkPolicy /
	// AuthorizationPolicy.
	// +optional
	Selector map[string]string `json:"selector,omitempty"`

	// Resolution is the DNS resolution mode of the companion ServiceEntry.
	// One of DNS, STATIC, DNS_ROUND_ROBIN, DYNAMIC_DNS, NONE. Default DNS.
	// +optional
	Resolution string `json:"resolution,omitempty"`

	// Labels / Annotations are applied to every resource the route emits.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// OutboundRoute is the typed view of one entry under `routes.outbound.<name>`.
// The generator emits one ServiceEntry per outbound route.
type OutboundRoute struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Hosts are the external hostnames the ServiceEntry registers.
	Hosts []string `json:"hosts,omitempty"`

	// Location is MESH_EXTERNAL (default) or MESH_INTERNAL.
	// +optional
	Location string `json:"location,omitempty"`

	// Resolution mirrors InboundRoute.Resolution. Default DNS.
	// +optional
	Resolution string `json:"resolution,omitempty"`

	// Ports describe the ServiceEntry's exposed ports.
	// +optional
	Ports []OutboundRoutePort `json:"ports,omitempty"`

	// +optional
	Metadata *RouteMetadata `json:"metadata,omitempty"`
}

// OutboundRoutePort is one entry in OutboundRoute.Ports.
type OutboundRoutePort struct {
	Number   uint32 `json:"number"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

// RouteMetadata holds labels/annotations attached to a route's emitted
// resources.
type RouteMetadata struct {
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}
