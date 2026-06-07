package generator

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// defaultEgressExcludeCIDRs is the bb-common default — AWS/GCP instance
// metadata service. Stripped from every egress ipBlock that contains it.
var defaultEgressExcludeCIDRs = []string{"169.254.169.254/32"}

// generateNetworkPolicies renders default NetworkPolicies, shorthand
// egress/ingress, and raw policies declared under `additionalPolicies[]`.
func generateNetworkPolicies(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio) ([]client.Object, error) {
	var out []client.Object

	out = append(out, defaultEgressPolicies(pkg, spec, istio)...)
	out = append(out, defaultIngressPolicies(pkg, spec)...)

	if spec.Egress != nil {
		objs, err := expandShorthandEgress(pkg, spec)
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
	}
	if spec.Ingress != nil {
		objs, err := expandShorthandIngress(pkg, spec)
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
	}

	for _, raw := range spec.AdditionalPolicies {
		np, err := buildAdditionalPolicy(pkg, spec, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, np)
	}
	for _, raw := range spec.Additional { // legacy alias
		np, err := buildAdditionalPolicy(pkg, spec, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, np)
	}

	return out, nil
}

// shorthandSource is one decoded `egress.from.<src>` or `ingress.to.<dst>`
// outer-map value. The inner `to.k8s` / `from.k8s` maps use the same
// pattern: `<key> -> true | { enabled, podSelector?, namespaceSelector? }`.
//
// The custom unmarshaler tolerates two podSelector shapes (matches what
// bb-common accepts):
//   - flat:   podSelector: {app: foo}
//   - nested: podSelector: {matchLabels: {app: foo}}
type shorthandSource struct {
	PodSelector map[string]string `json:"-"`
	To          *shorthandPeer    `json:"to,omitempty"`   // egress
	From        *shorthandPeer    `json:"from,omitempty"` // ingress
}

func (s *shorthandSource) UnmarshalJSON(b []byte) error {
	var raw struct {
		PodSelector map[string]interface{} `json:"podSelector,omitempty"`
		To          *shorthandPeer         `json:"to,omitempty"`
		From        *shorthandPeer         `json:"from,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	s.PodSelector = flattenMatchLabels(raw.PodSelector)
	s.To = raw.To
	s.From = raw.From
	return nil
}

type shorthandPeer struct {
	K8s        map[string]shorthandTarget `json:"k8s,omitempty"`
	Definition map[string]shorthandTarget `json:"definition,omitempty"`
	Cidr       map[string]shorthandTarget `json:"cidr,omitempty"`
	// Literal generator (raw spec passthrough under `to`/`from`) is
	// deferred — additionalPolicies[] covers the same need.
}

// shorthandTarget accepts either a bool (the common "true" form) or an
// object with `enabled` and selector overrides. Custom unmarshaler handles
// both.
type shorthandTarget struct {
	Enabled           bool              `json:"enabled,omitempty"`
	PodSelector       map[string]string `json:"-"`
	NamespaceSelector map[string]string `json:"-"`
}

func (t *shorthandTarget) UnmarshalJSON(b []byte) error {
	if len(b) == 4 && string(b) == "true" {
		t.Enabled = true
		return nil
	}
	if len(b) == 5 && string(b) == "false" {
		t.Enabled = false
		return nil
	}
	// Object form. Tolerate matchLabels nesting on the selectors.
	var raw struct {
		Enabled           *bool                  `json:"enabled,omitempty"`
		PodSelector       map[string]interface{} `json:"podSelector,omitempty"`
		NamespaceSelector map[string]interface{} `json:"namespaceSelector,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	t.Enabled = raw.Enabled == nil || *raw.Enabled
	t.PodSelector = flattenMatchLabels(raw.PodSelector)
	t.NamespaceSelector = flattenMatchLabels(raw.NamespaceSelector)
	return nil
}

func flattenMatchLabels(m map[string]interface{}) map[string]string {
	if m == nil {
		return nil
	}
	if ml, ok := m["matchLabels"].(map[string]interface{}); ok {
		out := make(map[string]string, len(ml))
		for k, v := range ml {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	// Allow flat `{key: value}` too.
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func expandShorthandEgress(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies) ([]client.Object, error) {
	prepend := spec.PrependReleaseName
	npLabels := defaultNetpolLabels("egress")
	var out []client.Object
	for _, localKey := range sortedKeys(spec.Egress.From) {
		var local shorthandSource
		if err := json.Unmarshal(spec.Egress.From[localKey].Raw, &local); err != nil {
			return nil, fmt.Errorf("networkPolicies.egress.from.%s: %w", localKey, err)
		}
		if local.To == nil {
			continue
		}
		for _, remoteKey := range sortedKeys(local.To.K8s) {
			target := local.To.K8s[remoteKey]
			if !target.Enabled {
				continue
			}
			remote, err := parseEgressRemoteKey(remoteKey)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.egress.from.%s.to.k8s: %w", localKey, err)
			}
			np := buildShorthandEgressNetpol(pkg, prepend, npLabels, localKey, local, remoteKey, remote, target)
			out = append(out, np)
		}
		for _, defName := range sortedKeys(local.To.Definition) {
			if !local.To.Definition[defName].Enabled {
				continue
			}
			def, err := resolveEgressDefinition(spec, defName)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.egress.from.%s.to.definition: %w", localKey, err)
			}
			np := buildEgressDefinitionNetpol(pkg, prepend, npLabels, localKey, local, defName, def)
			out = append(out, np)
		}
		for _, cidrKey := range sortedKeys(local.To.Cidr) {
			if !local.To.Cidr[cidrKey].Enabled {
				continue
			}
			cidr, err := parseEgressCIDRKey(cidrKey)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.egress.from.%s.to.cidr: %w", localKey, err)
			}
			np := buildEgressCIDRNetpol(pkg, spec, prepend, npLabels, localKey, local, cidrKey, cidr)
			out = append(out, np)
		}
	}
	return out, nil
}

func expandShorthandIngress(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies) ([]client.Object, error) {
	prepend := spec.PrependReleaseName
	npLabels := defaultNetpolLabels("ingress")
	var out []client.Object
	for _, localKey := range sortedKeys(spec.Ingress.To) {
		var local shorthandSource
		if err := json.Unmarshal(spec.Ingress.To[localKey].Raw, &local); err != nil {
			return nil, fmt.Errorf("networkPolicies.ingress.to.%s: %w", localKey, err)
		}
		if local.From == nil {
			continue
		}
		parsedLocal, err := parseIngressLocalKey(localKey)
		if err != nil {
			return nil, fmt.Errorf("networkPolicies.ingress.to: %w", err)
		}
		for _, remoteKey := range sortedKeys(local.From.K8s) {
			target := local.From.K8s[remoteKey]
			if !target.Enabled {
				continue
			}
			remote, err := parseIngressRemoteKey(remoteKey)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.ingress.to.%s.from.k8s: %w", localKey, err)
			}
			np := buildShorthandIngressNetpol(pkg, prepend, npLabels, parsedLocal, local, remoteKey, remote, target)
			out = append(out, np)
		}
		for _, defName := range sortedKeys(local.From.Definition) {
			if !local.From.Definition[defName].Enabled {
				continue
			}
			def, err := resolveIngressDefinition(spec, defName)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.ingress.to.%s.from.definition: %w", localKey, err)
			}
			np := buildIngressDefinitionNetpol(pkg, prepend, npLabels, parsedLocal, local, defName, def)
			out = append(out, np)
		}
		for _, cidrKey := range sortedKeys(local.From.Cidr) {
			if !local.From.Cidr[cidrKey].Enabled {
				continue
			}
			cidr, err := parseIngressCIDRKey(cidrKey)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.ingress.to.%s.from.cidr: %w", localKey, err)
			}
			np := buildIngressCIDRNetpol(pkg, prepend, npLabels, parsedLocal, local, cidrKey, cidr)
			out = append(out, np)
		}
	}
	return out, nil
}

func buildShorthandEgressNetpol(pkg *bbv1alpha1.Package, prepend bool, npLabels map[string]string, localKey string, local shorthandSource, remoteKey string, remote *parsedK8sRemote, target shorthandTarget) *networkingv1.NetworkPolicy {
	// Local pod selector — the "source" pod the rule applies to.
	srcSelector := metav1.LabelSelector{}
	if localKey != "*" {
		srcSelector.MatchLabels = map[string]string{"app.kubernetes.io/name": localKey}
	}
	if len(local.PodSelector) > 0 {
		srcSelector = metav1.LabelSelector{MatchLabels: local.PodSelector}
	}

	// Remote selectors with override support.
	remoteNS := remoteNamespaceSelector(remote.Namespace, target.NamespaceSelector)
	remotePod := remotePodSelector(remote.Pod, target.PodSelector)

	// Local name prefix and segments per bb-common.
	localName := localKey
	if localName == "*" {
		localName = "any-pod"
	}
	name := fmt.Sprintf("allow-egress-from-%s", localName)
	if remote.Namespace == "*" {
		name += "-to-any-ns"
	} else {
		name += "-to-ns-" + remote.Namespace
	}
	if remote.Pod != "" && remote.Pod != "*" {
		name += "-pod-" + remote.Pod
	} else {
		name += "-any-pod"
	}
	if len(remote.Ports) > 0 {
		name += "-" + strings.ToLower(remote.Protocol)
	}
	name += "-" + namePortSuffix(remote.Ports, remote.HasPortRange)
	name = prependName(prepend, pkg.Name, name)

	ports := buildNetpolPorts(remote.Protocol, remote.Ports, remote.HasPortRange)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":  localKey,
				"generated.network-policies.bigbang.dev/remote-key": remoteKey,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: srcSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To:    []networkingv1.NetworkPolicyPeer{{NamespaceSelector: remoteNS, PodSelector: remotePod}},
				Ports: ports,
			}},
		},
	}
}

func buildShorthandIngressNetpol(pkg *bbv1alpha1.Package, prepend bool, npLabels map[string]string, parsedLocal *parsedLocalIngressKey, local shorthandSource, remoteKey string, remote *parsedK8sRemote, target shorthandTarget) *networkingv1.NetworkPolicy {
	// Local pod selector — the "destination" pod the rule applies to.
	dstSelector := metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": parsedLocal.Pod}}
	if len(local.PodSelector) > 0 {
		dstSelector = metav1.LabelSelector{MatchLabels: local.PodSelector}
	}

	remoteNS := remoteNamespaceSelector(remote.Namespace, target.NamespaceSelector)
	remotePod := remotePodSelector(remote.Pod, target.PodSelector)

	name := fmt.Sprintf("allow-ingress-to-%s", parsedLocal.Pod)
	if parsedLocal.Protocol != "" && parsedLocal.Protocol != "TCP" {
		name += "-" + strings.ToLower(parsedLocal.Protocol)
	}
	if len(parsedLocal.Ports) > 0 {
		name += "-" + strings.ToLower(parsedLocal.Protocol)
	}
	name += "-" + namePortSuffix(parsedLocal.Ports, parsedLocal.HasPortRange)
	if remote.Namespace == "*" {
		name += "-from-any-ns"
	} else {
		name += "-from-ns-" + remote.Namespace
	}
	if remote.Pod != "" && remote.Pod != "*" {
		name += "-pod-" + remote.Pod
	} else {
		name += "-any-pod"
	}
	name = prependName(prepend, pkg.Name, name)

	ports := buildNetpolPorts(parsedLocal.Protocol, parsedLocal.Ports, parsedLocal.HasPortRange)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":  parsedLocal.Pod,
				"generated.network-policies.bigbang.dev/remote-key": remoteKey,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: dstSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{NamespaceSelector: remoteNS, PodSelector: remotePod}},
				Ports: ports,
			}},
		},
	}
}

// buildEgressCIDRNetpol emits the NetworkPolicy generated by
// `egress.from.<localKey>.to.cidr.<cidrKey>`. ipBlock.except is filled
// from spec.egress.excludeCIDRs (default ["169.254.169.254/32"]); each
// exclusion is added only if it is strictly contained in the rule's CIDR.
func buildEgressCIDRNetpol(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, prepend bool, npLabels map[string]string, localKey string, local shorthandSource, cidrKey string, cidr *parsedCIDR) *networkingv1.NetworkPolicy {
	srcSelector := metav1.LabelSelector{}
	if localKey != "*" {
		srcSelector.MatchLabels = map[string]string{"app.kubernetes.io/name": localKey}
	}
	if len(local.PodSelector) > 0 {
		srcSelector = metav1.LabelSelector{MatchLabels: local.PodSelector}
	}

	localName := localKey
	if localName == "*" {
		localName = "any-pod"
	}
	name := fmt.Sprintf("allow-egress-from-%s", localName)
	if cidr.CIDR == "0.0.0.0/0" {
		name += "-to-anywhere"
	} else {
		name += "-to-cidr-" + cidrNameSegment(cidr.CIDR)
	}
	if len(cidr.Ports) > 0 {
		name += "-" + strings.ToLower(cidr.Protocol)
	}
	name += "-" + namePortSuffix(cidr.Ports, cidr.HasPortRange)
	name = prependName(prepend, pkg.Name, name)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":  localKey,
				"generated.network-policies.bigbang.dev/remote-key": cidrKey,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: srcSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{
					CIDR:   cidr.CIDR,
					Except: applyExcludeCIDRs(cidr.CIDR, excludeCIDRsFor(spec)),
				}}},
				Ports: buildNetpolPorts(cidr.Protocol, cidr.Ports, cidr.HasPortRange),
			}},
		},
	}
}

// buildIngressCIDRNetpol emits the NetworkPolicy generated by
// `ingress.to.<localKey>.from.cidr.<cidrKey>`. Ports come from the local
// ingress key (parsed before this is called).
func buildIngressCIDRNetpol(pkg *bbv1alpha1.Package, prepend bool, npLabels map[string]string, parsedLocal *parsedLocalIngressKey, local shorthandSource, cidrKey string, cidr *parsedCIDR) *networkingv1.NetworkPolicy {
	dstSelector := metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": parsedLocal.Pod}}
	if len(local.PodSelector) > 0 {
		dstSelector = metav1.LabelSelector{MatchLabels: local.PodSelector}
	}

	name := fmt.Sprintf("allow-ingress-to-%s", parsedLocal.Pod)
	if parsedLocal.Protocol != "" && parsedLocal.Protocol != "TCP" {
		name += "-" + strings.ToLower(parsedLocal.Protocol)
	}
	if len(parsedLocal.Ports) > 0 {
		name += "-" + strings.ToLower(parsedLocal.Protocol) + "-" + namePortSuffix(parsedLocal.Ports, parsedLocal.HasPortRange)
	}
	if cidr.CIDR == "0.0.0.0/0" {
		name += "-from-anywhere"
	} else {
		name += "-from-cidr-" + cidrNameSegment(cidr.CIDR)
	}
	name = prependName(prepend, pkg.Name, name)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":  parsedLocal.Pod,
				"generated.network-policies.bigbang.dev/remote-key": cidrKey,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: dstSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: cidr.CIDR}}},
				Ports: buildNetpolPorts(parsedLocal.Protocol, parsedLocal.Ports, parsedLocal.HasPortRange),
			}},
		},
	}
}

// excludeCIDRsFor returns the configured exclusion list, falling back to
// the bb-common default. An explicitly-empty list disables exclusion.
func excludeCIDRsFor(spec *bbv1alpha1.NetworkPolicies) []string {
	if spec == nil || spec.Egress == nil || spec.Egress.ExcludeCIDRs == nil {
		return defaultEgressExcludeCIDRs
	}
	return spec.Egress.ExcludeCIDRs
}

// applyExcludeCIDRs returns the subset of `exclusions` strictly contained
// in `cidr`. An exclusion identical to `cidr` is skipped (no point excluding
// the whole rule). Malformed CIDRs are ignored — the rule still emits.
func applyExcludeCIDRs(cidr string, exclusions []string) []string {
	if len(exclusions) == 0 {
		return nil
	}
	_, ruleNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(exclusions))
	for _, ex := range exclusions {
		if ex == cidr {
			continue
		}
		exIP, exNet, err := net.ParseCIDR(ex)
		if err != nil {
			continue
		}
		ruleOnes, _ := ruleNet.Mask.Size()
		exOnes, _ := exNet.Mask.Size()
		if exOnes < ruleOnes {
			continue // exclusion is broader than the rule
		}
		if !ruleNet.Contains(exIP) {
			continue
		}
		out = append(out, ex)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func remoteNamespaceSelector(ns string, override map[string]string) *metav1.LabelSelector {
	if len(override) > 0 {
		return &metav1.LabelSelector{MatchLabels: override}
	}
	if ns == "*" {
		return &metav1.LabelSelector{}
	}
	return &metav1.LabelSelector{
		MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns},
	}
}

func remotePodSelector(pod string, override map[string]string) *metav1.LabelSelector {
	if len(override) > 0 {
		return &metav1.LabelSelector{MatchLabels: override}
	}
	if pod == "" || pod == "*" {
		return &metav1.LabelSelector{}
	}
	return &metav1.LabelSelector{
		MatchLabels: map[string]string{"app.kubernetes.io/name": pod},
	}
}

func buildNetpolPorts(protocol string, ports []int, hasRange bool) []networkingv1.NetworkPolicyPort {
	if len(ports) == 0 {
		return nil
	}
	proto := corev1.ProtocolTCP
	if strings.ToUpper(protocol) == "UDP" {
		proto = corev1.ProtocolUDP
	}
	if hasRange && len(ports) == 2 {
		begin := intstr.FromInt(ports[0])
		end := int32(ports[1])
		return []networkingv1.NetworkPolicyPort{{Protocol: &proto, Port: &begin, EndPort: &end}}
	}
	out := make([]networkingv1.NetworkPolicyPort, 0, len(ports))
	for _, p := range ports {
		port := intstr.FromInt(p)
		out = append(out, networkingv1.NetworkPolicyPort{Protocol: &proto, Port: &port})
	}
	return out
}

func defaultEgressPolicies(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio) []client.Object {
	prepend := spec.PrependReleaseName
	npLabels := defaultNetpolLabels("egress")
	var out []client.Object

	if egressDenyAll(spec) {
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-egress-deny-all"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			},
		})
	}
	if egressAllowInNS(spec) {
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-egress-allow-all-in-ns"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To: []networkingv1.NetworkPolicyPeer{{
						PodSelector: &metav1.LabelSelector{},
					}},
				}},
			},
		})
	}
	if egressAllowKubeDNS(spec) {
		port53 := intstr.FromInt(53)
		udp := corev1.ProtocolUDP
		tcp := corev1.ProtocolTCP
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-egress-allow-kube-dns"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
						},
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"k8s-app": "kube-dns"},
						},
					}},
					Ports: []networkingv1.NetworkPolicyPort{
						{Protocol: &udp, Port: &port53},
						{Protocol: &tcp, Port: &port53},
					},
				}},
			},
		})
	}
	if egressAllowIstiod(spec) && !istioAmbient(istio) {
		port15012 := intstr.FromInt(15012)
		tcp := corev1.ProtocolTCP
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-egress-allow-istiod"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"kubernetes.io/metadata.name": "istio-system"},
						},
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "istiod"},
						},
					}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port15012}},
				}},
			},
		})
	}
	return out
}

func defaultIngressPolicies(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies) []client.Object {
	prepend := spec.PrependReleaseName
	npLabels := defaultNetpolLabels("ingress")
	var out []client.Object

	if ingressDenyAll(spec) {
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-ingress-deny-all"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			},
		})
	}
	if ingressAllowInNS(spec) {
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-ingress-allow-all-in-ns"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{{
						PodSelector: &metav1.LabelSelector{},
					}},
				}},
			},
		})
	}
	if ingressAllowPromToSidecar(spec) {
		port15020 := intstr.FromInt(15020)
		tcp := corev1.ProtocolTCP
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-ingress-allow-prometheus-to-istio-sidecar"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"kubernetes.io/metadata.name": "monitoring"},
						},
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app.kubernetes.io/name": "prometheus"},
						},
					}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port15020}},
				}},
			},
		})
	}
	return out
}

func buildAdditionalPolicy(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, raw bbv1alpha1.AdditionalPolicy) (client.Object, error) {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        prependName(spec.PrependReleaseName, pkg.Name, raw.Name),
			Labels:      map[string]string(raw.Labels),
			Annotations: map[string]string(raw.Annotations),
		},
	}
	if len(raw.Spec) > 0 {
		b, err := json.Marshal(raw.Spec)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &np.Spec); err != nil {
			return nil, err
		}
	}
	return np, nil
}

// defaultEnabled returns true unless the *bool is explicitly false. Nil is
// treated as enabled, matching bb-common's implicit-true behavior.
func defaultEnabled(b *bool) bool {
	return b == nil || *b
}

// egressSideEnabled returns true when egress defaults aren't explicitly
// disabled (egress.defaults.enabled: false collapses all egress defaults).
func egressSideEnabled(spec *bbv1alpha1.NetworkPolicies) bool {
	if spec.Egress == nil || spec.Egress.Defaults == nil {
		return true
	}
	return defaultEnabled(spec.Egress.Defaults.Enabled)
}

// ingressSideEnabled is the ingress counterpart of egressSideEnabled.
func ingressSideEnabled(spec *bbv1alpha1.NetworkPolicies) bool {
	if spec.Ingress == nil || spec.Ingress.Defaults == nil {
		return true
	}
	return defaultEnabled(spec.Ingress.Defaults.Enabled)
}

// Per-default getters: each returns true unless the per-default is
// explicitly disabled. Missing sub-structs are treated as enabled.
func egressDenyAll(spec *bbv1alpha1.NetworkPolicies) bool {
	if !egressSideEnabled(spec) || spec.Egress == nil || spec.Egress.Defaults == nil || spec.Egress.Defaults.DenyAll == nil {
		return egressSideEnabled(spec)
	}
	return defaultEnabled(spec.Egress.Defaults.DenyAll.Enabled)
}

func egressAllowInNS(spec *bbv1alpha1.NetworkPolicies) bool {
	if !egressSideEnabled(spec) || spec.Egress == nil || spec.Egress.Defaults == nil || spec.Egress.Defaults.AllowInNamespace == nil {
		return egressSideEnabled(spec)
	}
	return defaultEnabled(spec.Egress.Defaults.AllowInNamespace.Enabled)
}

func egressAllowKubeDNS(spec *bbv1alpha1.NetworkPolicies) bool {
	if !egressSideEnabled(spec) || spec.Egress == nil || spec.Egress.Defaults == nil || spec.Egress.Defaults.AllowKubeDNS == nil {
		return egressSideEnabled(spec)
	}
	return defaultEnabled(spec.Egress.Defaults.AllowKubeDNS.Enabled)
}

func egressAllowIstiod(spec *bbv1alpha1.NetworkPolicies) bool {
	if !egressSideEnabled(spec) || spec.Egress == nil || spec.Egress.Defaults == nil || spec.Egress.Defaults.AllowIstiod == nil {
		return egressSideEnabled(spec)
	}
	return defaultEnabled(spec.Egress.Defaults.AllowIstiod.Enabled)
}

func ingressDenyAll(spec *bbv1alpha1.NetworkPolicies) bool {
	if !ingressSideEnabled(spec) || spec.Ingress == nil || spec.Ingress.Defaults == nil || spec.Ingress.Defaults.DenyAll == nil {
		return ingressSideEnabled(spec)
	}
	return defaultEnabled(spec.Ingress.Defaults.DenyAll.Enabled)
}

func ingressAllowInNS(spec *bbv1alpha1.NetworkPolicies) bool {
	if !ingressSideEnabled(spec) || spec.Ingress == nil || spec.Ingress.Defaults == nil || spec.Ingress.Defaults.AllowInNamespace == nil {
		return ingressSideEnabled(spec)
	}
	return defaultEnabled(spec.Ingress.Defaults.AllowInNamespace.Enabled)
}

func ingressAllowPromToSidecar(spec *bbv1alpha1.NetworkPolicies) bool {
	if !ingressSideEnabled(spec) || spec.Ingress == nil || spec.Ingress.Defaults == nil || spec.Ingress.Defaults.AllowPrometheusToIstioSidecar == nil {
		return ingressSideEnabled(spec)
	}
	return defaultEnabled(spec.Ingress.Defaults.AllowPrometheusToIstioSidecar.Enabled)
}

func defaultNetpolLabels(direction string) map[string]string {
	return map[string]string{
		LabelNetpolSource:                        LabelNetpolSourceValue,
		"network-policies.bigbang.dev/direction": direction,
	}
}

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func istioAmbient(istio *bbv1alpha1.Istio) bool {
	return istio != nil && istio.Ambient != nil && istio.Ambient.Enabled
}
