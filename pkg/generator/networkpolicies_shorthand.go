package generator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// Shorthand expansion: turns the parsed egress/ingress shorthand into
// NetworkPolicies (k8s, cidr, definition references are resolved in
// definitions.go; literal rules pass their spec through verbatim).

func expandShorthandEgress(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, env defsEnv) ([]client.Object, error) {
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
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, target.Metadata))
			out = append(out, np)
		}
		for _, defName := range sortedKeys(local.To.Definition) {
			target := local.To.Definition[defName]
			if !target.Enabled {
				continue
			}
			def, err := resolveEgressDefinition(spec, defName, env)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.egress.from.%s.to.definition: %w", localKey, err)
			}
			np := buildEgressDefinitionNetpol(pkg, prepend, npLabels, localKey, local, defName, def)
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, target.Metadata))
			out = append(out, np)
		}
		for _, cidrKey := range sortedKeys(local.To.Cidr) {
			target := local.To.Cidr[cidrKey]
			if !target.Enabled {
				continue
			}
			cidr, err := parseEgressCIDRKey(cidrKey)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.egress.from.%s.to.cidr: %w", localKey, err)
			}
			np := buildEgressCIDRNetpol(pkg, spec, prepend, npLabels, localKey, local, cidrKey, cidr)
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, target.Metadata))
			out = append(out, np)
		}
		for _, ruleKey := range sortedKeys(local.To.Literal) {
			lit := local.To.Literal[ruleKey]
			if !lit.Enabled {
				continue
			}
			np, err := buildEgressLiteralNetpol(pkg, prepend, npLabels, localKey, local, ruleKey, lit)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.egress.from.%s.to.literal: %w", localKey, err)
			}
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, lit.Metadata))
			out = append(out, np)
		}
	}
	return out, nil
}

func expandShorthandIngress(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, env defsEnv) ([]client.Object, error) {
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
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, target.Metadata))
			out = append(out, np)
		}
		for _, defName := range sortedKeys(local.From.Definition) {
			target := local.From.Definition[defName]
			if !target.Enabled {
				continue
			}
			def, err := resolveIngressDefinition(spec, defName, env)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.ingress.to.%s.from.definition: %w", localKey, err)
			}
			np := buildIngressDefinitionNetpol(pkg, prepend, npLabels, parsedLocal, local, defName, def)
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, target.Metadata))
			out = append(out, np)
		}
		for _, cidrKey := range sortedKeys(local.From.Cidr) {
			target := local.From.Cidr[cidrKey]
			if !target.Enabled {
				continue
			}
			cidr, err := parseIngressCIDRKey(cidrKey)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.ingress.to.%s.from.cidr: %w", localKey, err)
			}
			np := buildIngressCIDRNetpol(pkg, prepend, npLabels, parsedLocal, local, cidrKey, cidr)
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, target.Metadata))
			out = append(out, np)
		}
		for _, ruleKey := range sortedKeys(local.From.Literal) {
			lit := local.From.Literal[ruleKey]
			if !lit.Enabled {
				continue
			}
			np, err := buildIngressLiteralNetpol(pkg, prepend, npLabels, parsedLocal, local, ruleKey, lit)
			if err != nil {
				return nil, fmt.Errorf("networkPolicies.ingress.to.%s.from.literal: %w", localKey, err)
			}
			applyShorthandMetadata(np, mergeShorthandMetadata(local.Metadata, lit.Metadata))
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
		localName = nameAnyPod
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
	if parsedLocal.Protocol != "" && parsedLocal.Protocol != protoTCP {
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
		localName = nameAnyPod
	}
	name := fmt.Sprintf("allow-egress-from-%s", localName)
	if cidr.CIDR == cidrAnywhere {
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
	if parsedLocal.Protocol != "" && parsedLocal.Protocol != protoTCP {
		name += "-" + strings.ToLower(parsedLocal.Protocol)
	}
	if len(parsedLocal.Ports) > 0 {
		name += "-" + strings.ToLower(parsedLocal.Protocol) + "-" + namePortSuffix(parsedLocal.Ports, parsedLocal.HasPortRange)
	}
	if cidr.CIDR == cidrAnywhere {
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

// literalRuleKeyRe rejects keys that look like shorthand (contain `/`,
// `:`, `@`, ...) — a literal rule key is a plain name, per bb-common's
// from-spec-literal generator.
var literalRuleKeyRe = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// buildEgressLiteralNetpol emits the NetworkPolicy generated by
// `egress.from.<localKey>.to.literal.<ruleKey>`: the rule value's `spec`
// becomes the policy's egress rules verbatim (no excludeCIDRs, no port
// parsing), mirroring bb-common's from-spec-literal generator.
func buildEgressLiteralNetpol(pkg *bbv1alpha1.Package, prepend bool, npLabels map[string]string, localKey string, local shorthandSource, ruleKey string, lit literalTarget) (*networkingv1.NetworkPolicy, error) {
	if !literalRuleKeyRe.MatchString(ruleKey) {
		return nil, fmt.Errorf("rule key %q cannot combine shorthand syntax with a spec value", ruleKey)
	}
	rules, specYAML, err := decodeLiteralRules[networkingv1.NetworkPolicyEgressRule](lit.Spec)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ruleKey, err)
	}

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
	name := fmt.Sprintf("allow-egress-from-%s-to-%s", localName, strings.ToLower(ruleKey))
	name = prependName(prepend, pkg.Name, name)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":         localKey,
				"generated.network-policies.bigbang.dev/remote-key":        ruleKey,
				"generated.network-policies.bigbang.dev/from-spec-literal": specYAML,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: srcSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      rules,
		},
	}, nil
}

// buildIngressLiteralNetpol is the ingress counterpart of
// buildEgressLiteralNetpol. Ports from the local ingress key are ignored —
// the literal spec is authoritative.
func buildIngressLiteralNetpol(pkg *bbv1alpha1.Package, prepend bool, npLabels map[string]string, parsedLocal *parsedLocalIngressKey, local shorthandSource, ruleKey string, lit literalTarget) (*networkingv1.NetworkPolicy, error) {
	if !literalRuleKeyRe.MatchString(ruleKey) {
		return nil, fmt.Errorf("rule key %q cannot combine shorthand syntax with a spec value", ruleKey)
	}
	rules, specYAML, err := decodeLiteralRules[networkingv1.NetworkPolicyIngressRule](lit.Spec)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ruleKey, err)
	}

	dstSelector := metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": parsedLocal.Pod}}
	if len(local.PodSelector) > 0 {
		dstSelector = metav1.LabelSelector{MatchLabels: local.PodSelector}
	}

	name := fmt.Sprintf("allow-ingress-to-%s", parsedLocal.Pod)
	if parsedLocal.Protocol != "" && parsedLocal.Protocol != protoTCP {
		name += "-" + strings.ToLower(parsedLocal.Protocol)
	}
	name += "-from-" + strings.ToLower(ruleKey)
	name = prependName(prepend, pkg.Name, name)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: cloneLabels(npLabels),
			Annotations: map[string]string{
				"generated.network-policies.bigbang.dev/local-key":         parsedLocal.Pod,
				"generated.network-policies.bigbang.dev/remote-key":        ruleKey,
				"generated.network-policies.bigbang.dev/from-spec-literal": specYAML,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: dstSelector,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     rules,
		},
	}, nil
}

// decodeLiteralRules unmarshals a literal rule's raw `spec` array into typed
// rules and returns the YAML rendering stamped into the from-spec-literal
// annotation (bb-common records the spec as YAML there).
func decodeLiteralRules[T any](raw json.RawMessage) ([]T, string, error) {
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("literal rule has no spec")
	}
	var rules []T
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, "", fmt.Errorf("spec must be a rule array: %w", err)
	}
	specYAML, err := yaml.JSONToYAML(raw)
	if err != nil {
		return nil, "", err
	}
	return rules, strings.TrimRight(string(specYAML), "\n") + "\n", nil
}
