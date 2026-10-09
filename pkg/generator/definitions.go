package generator

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

func matchLabels(k, v string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
}

// resolvedDefinition is the NetworkPolicy-rule view of a named definition.
// `peers` populates the `to[]`/`from[]` of the generated rule; `ports`
// optionally restricts to specific ports.
type resolvedDefinition struct {
	peers []networkingv1.NetworkPolicyPeer
	ports []networkingv1.NetworkPolicyPort
}

// builtInEgressDefinitions mirrors bb-common's
// network-policies/egress/definitions/_default.tpl. bb-common restricts
// `kubeAPI` to the API server's ports via a render-time lookup of the
// `default/kubernetes` Service; the controller performs the same lookup at
// reconcile time and passes the target ports in. With no ports (lookup
// failed or unavailable) the definition allows all ports, exactly like
// bb-common when its lookup returns nothing.
func builtInEgressDefinitions(kubeAPIPorts []intstr.IntOrString) map[string]resolvedDefinition {
	tcp := corev1.ProtocolTCP
	var ports []networkingv1.NetworkPolicyPort
	for i := range kubeAPIPorts {
		ports = append(ports, networkingv1.NetworkPolicyPort{
			Port:     &kubeAPIPorts[i],
			Protocol: &tcp,
		})
	}
	return map[string]resolvedDefinition{
		"kubeAPI": {
			peers: []networkingv1.NetworkPolicyPeer{
				{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8"}},
				{IPBlock: &networkingv1.IPBlock{CIDR: "172.16.0.0/12"}},
				{IPBlock: &networkingv1.IPBlock{CIDR: "192.168.0.0/16"}},
			},
			ports: ports,
		},
	}
}

// builtInIngressDefinitions mirrors
// network-policies/ingress/definitions/_default.tpl.
func builtInIngressDefinitions() map[string]resolvedDefinition {
	return map[string]resolvedDefinition{
		"gateway": {
			peers: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: matchLabels("kubernetes.io/metadata.name", "istio-gateway"),
				PodSelector:       matchLabels("istio", "ingressgateway"),
			}},
		},
		"monitoring": {
			peers: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: matchLabels("kubernetes.io/metadata.name", "monitoring"),
				PodSelector:       matchLabels("app.kubernetes.io/name", "prometheus"),
			}},
		},
	}
}

// defsEnv carries the reconcile-time inputs that shape definition
// resolution: the kubeAPI port lookup and the shared pools from the
// operator's global ConfigMap.
type defsEnv struct {
	kubeAPIPorts  []intstr.IntOrString
	sharedEgress  map[string]bbv1alpha1.NetworkPoliciesEgressDefinitionsValue
	sharedIngress map[string]bbv1alpha1.NetworkPoliciesIngressDefinitionsValue
	sharedErr     error
}

// resolveEgressDefinition returns the definition for `name`. Layering on
// name collision: built-in < shared (global ConfigMap) < package-local.
// Unknown names produce an error so typos surface at reconcile.
func resolveEgressDefinition(spec *bbv1alpha1.NetworkPolicies, name string, env defsEnv) (*resolvedDefinition, error) {
	// Package-local first: it wins outright, and is the only layer safe to
	// resolve when the shared pool is unreadable.
	if spec.Egress != nil {
		if raw, ok := spec.Egress.Definitions[name]; ok {
			parsed, err := parseEgressDefinition(raw)
			if err != nil {
				return nil, fmt.Errorf("egress.definitions.%s: %w", name, err)
			}
			return parsed, nil
		}
	}
	if env.sharedErr != nil {
		// The shared pool could hold or override `name`; guessing from the
		// remaining layers would silently change which rule is emitted.
		return nil, fmt.Errorf("egress definition %q: global config unreadable: %w", name, env.sharedErr)
	}
	if raw, ok := env.sharedEgress[name]; ok {
		parsed, err := parseEgressDefinition(raw)
		if err != nil {
			return nil, fmt.Errorf("global egress definition %s: %w", name, err)
		}
		return parsed, nil
	}
	if d, ok := builtInEgressDefinitions(env.kubeAPIPorts)[name]; ok {
		return &d, nil
	}
	return nil, fmt.Errorf("egress definition %q not found", name)
}

// resolveIngressDefinition is the ingress counterpart, with the same
// built-in < shared < package-local layering.
func resolveIngressDefinition(spec *bbv1alpha1.NetworkPolicies, name string, env defsEnv) (*resolvedDefinition, error) {
	if spec.Ingress != nil {
		if raw, ok := spec.Ingress.Definitions[name]; ok {
			parsed, err := parseIngressDefinition(raw)
			if err != nil {
				return nil, fmt.Errorf("ingress.definitions.%s: %w", name, err)
			}
			return parsed, nil
		}
	}
	if env.sharedErr != nil {
		return nil, fmt.Errorf("ingress definition %q: global config unreadable: %w", name, env.sharedErr)
	}
	if raw, ok := env.sharedIngress[name]; ok {
		parsed, err := parseIngressDefinition(raw)
		if err != nil {
			return nil, fmt.Errorf("global ingress definition %s: %w", name, err)
		}
		return parsed, nil
	}
	if d, ok := builtInIngressDefinitions()[name]; ok {
		return &d, nil
	}
	return nil, fmt.Errorf("ingress definition %q not found", name)
}

// definitionPeer is the loose peer shape of one entry under
// `networkPolicies.{egress,ingress}.definitions.<name>`, translated from the
// schema-generated types by typedPeer. Selector maps are nil when the field
// was absent and non-nil (possibly empty) when authored — the distinction
// carries "match all" semantics, so peers must NOT round-trip through
// json.Marshal (omitempty drops empty maps, silently narrowing the rule).
type definitionPeer struct {
	IPBlock           *definitionIPBlock     `json:"ipBlock,omitempty"`
	NamespaceSelector map[string]interface{} `json:"namespaceSelector,omitempty"`
	PodSelector       map[string]interface{} `json:"podSelector,omitempty"`
}

type definitionIPBlock struct {
	CIDR   string   `json:"cidr,omitempty"`
	Except []string `json:"except,omitempty"`
}

type definitionPort struct {
	Port     *intstr.IntOrString `json:"port,omitempty"`
	EndPort  *int32              `json:"endPort,omitempty"`
	Protocol string              `json:"protocol,omitempty"`
}

func parseEgressDefinition(raw bbv1alpha1.NetworkPoliciesEgressDefinitionsValue) (*resolvedDefinition, error) {
	peers := make([]definitionPeer, 0, len(raw.To))
	for _, e := range raw.To {
		var cidr *string
		var except []string
		if e.IPBlock != nil {
			cidr, except = e.IPBlock.CIDR, e.IPBlock.Except
		}
		p, err := typedPeer(cidr, except, e.NamespaceSelector, e.PodSelector)
		if err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	ports, err := parseDefinitionPorts(raw.Ports)
	if err != nil {
		return nil, err
	}
	return buildDefinition(peers, ports)
}

func parseIngressDefinition(raw bbv1alpha1.NetworkPoliciesIngressDefinitionsValue) (*resolvedDefinition, error) {
	peers := make([]definitionPeer, 0, len(raw.From))
	for _, e := range raw.From {
		var cidr *string
		var except []string
		if e.IPBlock != nil {
			cidr, except = e.IPBlock.CIDR, e.IPBlock.Except
		}
		p, err := typedPeer(cidr, except, e.NamespaceSelector, e.PodSelector)
		if err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	ports, err := parseDefinitionPorts(raw.Ports)
	if err != nil {
		return nil, err
	}
	return buildDefinition(peers, ports)
}

// typedPeer converts one schema-generated peer into a definitionPeer without
// a JSON round-trip, so absent vs explicitly-empty selectors stay distinct.
func typedPeer(cidr *string, except []string, ns, pod map[string]apiextensionsv1.JSON) (definitionPeer, error) {
	p := definitionPeer{}
	if cidr != nil || except != nil {
		p.IPBlock = &definitionIPBlock{Except: except}
		if cidr != nil {
			p.IPBlock.CIDR = *cidr
		}
	}
	var err error
	if p.NamespaceSelector, err = selectorFromTyped(ns); err != nil {
		return p, err
	}
	if p.PodSelector, err = selectorFromTyped(pod); err != nil {
		return p, err
	}
	return p, nil
}

func selectorFromTyped(m map[string]apiextensionsv1.JSON) (map[string]interface{}, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		var val interface{}
		if err := json.Unmarshal(v.Raw, &val); err != nil {
			return nil, fmt.Errorf("selector key %s: %w", k, err)
		}
		out[k] = val
	}
	return out, nil
}

func parseDefinitionPorts(typed interface{}) ([]definitionPort, error) {
	b, err := json.Marshal(typed)
	if err != nil {
		return nil, err
	}
	var out []definitionPort
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// buildEgressDefinitionNetpol emits the NetworkPolicy generated by
// `egress.from.<localKey>.to.definition.<defName>`.
func buildEgressDefinitionNetpol(pkg *bbv1alpha1.Package, prepend bool, npLabels map[string]string, localKey string, local shorthandSource, defName string, def *resolvedDefinition) *networkingv1.NetworkPolicy {
	srcSelector := metav1.LabelSelector{}
	if localKey != "*" {
		srcSelector.MatchLabels = map[string]string{"app.kubernetes.io/name": localKey}
	}
	if len(local.PodSelector) > 0 {
		srcSelector = metav1.LabelSelector{MatchLabels: local.PodSelector}
	}

	localName := localKey
	if localName == "*" {
		localName = nameAnyPod
	}
	name := prependName(prepend, pkg.Name, fmt.Sprintf("allow-egress-from-%s-to-%s", localName, strings.ToLower(defName)))

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":       localKey,
				"generated.network-policies.bigbang.dev/from-definition": defName,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: srcSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To:    def.peers,
				Ports: def.ports,
			}},
		},
	}
}

// buildIngressDefinitionNetpol emits the NetworkPolicy generated by
// `ingress.to.<localKey>.from.definition.<defName>`. Per-call ports
// (from the local key) override the definition's ports.
func buildIngressDefinitionNetpol(pkg *bbv1alpha1.Package, prepend bool, npLabels map[string]string, parsedLocal *parsedLocalIngressKey, local shorthandSource, defName string, def *resolvedDefinition) *networkingv1.NetworkPolicy {
	dstSelector := metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": parsedLocal.Pod}}
	if len(local.PodSelector) > 0 {
		dstSelector = metav1.LabelSelector{MatchLabels: local.PodSelector}
	}

	ports := def.ports
	if len(parsedLocal.Ports) > 0 {
		ports = buildNetpolPorts(parsedLocal.Protocol, parsedLocal.Ports, parsedLocal.HasPortRange)
	}

	name := fmt.Sprintf("allow-ingress-to-%s", parsedLocal.Pod)
	if parsedLocal.Protocol != "" && parsedLocal.Protocol != protoTCP {
		name += "-" + strings.ToLower(parsedLocal.Protocol)
	}
	if len(parsedLocal.Ports) > 0 {
		name += "-" + strings.ToLower(parsedLocal.Protocol) + "-" + namePortSuffix(parsedLocal.Ports, parsedLocal.HasPortRange)
	}
	name = prependName(prepend, pkg.Name, name+"-from-"+strings.ToLower(defName))

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":       parsedLocal.Pod,
				"generated.network-policies.bigbang.dev/from-definition": defName,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: dstSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  def.peers,
				Ports: ports,
			}},
		},
	}
}

func buildDefinition(peersRaw []definitionPeer, portsRaw []definitionPort) (*resolvedDefinition, error) {
	out := &resolvedDefinition{}
	for _, p := range peersRaw {
		peer := networkingv1.NetworkPolicyPeer{}
		if p.IPBlock != nil {
			peer.IPBlock = &networkingv1.IPBlock{CIDR: p.IPBlock.CIDR, Except: p.IPBlock.Except}
		}
		// Presence-based, not len-based: an explicit empty selector ({})
		// means "match all" and must survive to the NetworkPolicy — dropping
		// it flips namespaceSelector: {} from any-namespace to same-namespace.
		if p.NamespaceSelector != nil {
			peer.NamespaceSelector = &metav1.LabelSelector{MatchLabels: flattenMatchLabels(p.NamespaceSelector)}
		}
		if p.PodSelector != nil {
			peer.PodSelector = &metav1.LabelSelector{MatchLabels: flattenMatchLabels(p.PodSelector)}
		}
		out.peers = append(out.peers, peer)
	}
	for _, p := range portsRaw {
		np := networkingv1.NetworkPolicyPort{}
		if p.Port != nil {
			pv := *p.Port
			np.Port = &pv
		}
		if p.EndPort != nil {
			ep := *p.EndPort
			np.EndPort = &ep
		}
		if p.Protocol != "" {
			proto := corev1.Protocol(p.Protocol)
			np.Protocol = &proto
		}
		out.ports = append(out.ports, np)
	}
	return out, nil
}
