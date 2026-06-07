package generator

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

func ls(k, v string) *metav1.LabelSelector {
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
// network-policies/egress/definitions/_default.tpl. `kubeAPI` here omits the
// ports list because bb-common populates it at render time via a live lookup
// of the kubernetes Service — the operator can't replicate that cleanly, so
// callers must override the definition if they need a port filter.
func builtInEgressDefinitions() map[string]resolvedDefinition {
	return map[string]resolvedDefinition{
		"kubeAPI": {
			peers: []networkingv1.NetworkPolicyPeer{
				{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8"}},
				{IPBlock: &networkingv1.IPBlock{CIDR: "172.16.0.0/12"}},
				{IPBlock: &networkingv1.IPBlock{CIDR: "192.168.0.0/16"}},
			},
		},
	}
}

// builtInIngressDefinitions mirrors
// network-policies/ingress/definitions/_default.tpl.
func builtInIngressDefinitions() map[string]resolvedDefinition {
	return map[string]resolvedDefinition{
		"gateway": {
			peers: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: ls("kubernetes.io/metadata.name", "istio-gateway"),
				PodSelector:       ls("istio", "ingressgateway"),
			}},
		},
		"monitoring": {
			peers: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: ls("kubernetes.io/metadata.name", "monitoring"),
				PodSelector:       ls("app.kubernetes.io/name", "prometheus"),
			}},
		},
	}
}

// resolveEgressDefinition returns the built-in definition for `name`, with
// any user override (under `networkPolicies.egress.definitions.<name>`)
// applied. Unknown names produce an error so typos surface at reconcile.
func resolveEgressDefinition(spec *bbv1alpha1.NetworkPolicies, name string) (*resolvedDefinition, error) {
	defs := builtInEgressDefinitions()
	if spec.Egress != nil {
		for k, raw := range spec.Egress.Definitions {
			parsed, err := parseEgressDefinition(raw)
			if err != nil {
				return nil, fmt.Errorf("egress.definitions.%s: %w", k, err)
			}
			defs[k] = *parsed
		}
	}
	d, ok := defs[name]
	if !ok {
		return nil, fmt.Errorf("egress definition %q not found", name)
	}
	return &d, nil
}

// resolveIngressDefinition is the ingress counterpart.
func resolveIngressDefinition(spec *bbv1alpha1.NetworkPolicies, name string) (*resolvedDefinition, error) {
	defs := builtInIngressDefinitions()
	if spec.Ingress != nil {
		for k, raw := range spec.Ingress.Definitions {
			parsed, err := parseIngressDefinition(raw)
			if err != nil {
				return nil, fmt.Errorf("ingress.definitions.%s: %w", k, err)
			}
			defs[k] = *parsed
		}
	}
	d, ok := defs[name]
	if !ok {
		return nil, fmt.Errorf("ingress definition %q not found", name)
	}
	return &d, nil
}

// definitionRaw is the loose shape of one entry under
// `networkPolicies.{egress,ingress}.definitions.<name>` as authored in YAML.
// The schema-generated types are too rigid for the operator's freeform decode
// (e.g. they pin selectors to apiextensionsv1.JSON maps); we accept the raw
// JSON and translate to networkingv1 peers ourselves.
type definitionRaw struct {
	Ports []definitionPort `json:"ports,omitempty"`
	To    []definitionPeer `json:"to,omitempty"`
	From  []definitionPeer `json:"from,omitempty"`
}

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
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var r definitionRaw
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return buildDefinition(r.To, r.Ports)
}

func parseIngressDefinition(raw bbv1alpha1.NetworkPoliciesIngressDefinitionsValue) (*resolvedDefinition, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var r definitionRaw
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return buildDefinition(r.From, r.Ports)
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
	name := prependName(prepend, pkg.Name, fmt.Sprintf("allow-egress-from-%s-to-%s", localName, lowercase(defName)))

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
		name += "-" + lowercase(parsedLocal.Protocol)
	}
	if len(parsedLocal.Ports) > 0 {
		name += "-" + lowercase(parsedLocal.Protocol) + "-" + namePortSuffix(parsedLocal.Ports, parsedLocal.HasPortRange)
	}
	name = prependName(prepend, pkg.Name, name+"-from-"+lowercase(defName))

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
		if ns := flattenMatchLabels(p.NamespaceSelector); len(ns) > 0 {
			peer.NamespaceSelector = &metav1.LabelSelector{MatchLabels: ns}
		}
		if pod := flattenMatchLabels(p.PodSelector); len(pod) > 0 {
			peer.PodSelector = &metav1.LabelSelector{MatchLabels: pod}
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
