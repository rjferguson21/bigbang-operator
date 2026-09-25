package generator

import (
	"encoding/json"
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
func generateNetworkPolicies(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio, kubeAPIPorts []intstr.IntOrString) ([]client.Object, error) {
	var out []client.Object

	out = append(out, defaultEgressPolicies(pkg, spec, istio)...)
	out = append(out, defaultIngressPolicies(pkg, spec, istio)...)

	if spec.Egress != nil {
		objs, err := expandShorthandEgress(pkg, spec, kubeAPIPorts)
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

func defaultNetpolLabels(direction string) map[string]string {
	return map[string]string{
		LabelNetpolSource:                        LabelNetpolSourceValue,
		"network-policies.bigbang.dev/direction": direction,
	}
}
