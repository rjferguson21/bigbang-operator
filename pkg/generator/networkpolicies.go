package generator

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// generateNetworkPolicies renders default NetworkPolicies and any raw
// policies declared under `additionalPolicies[]`.
//
// v1 scope: defaults + additionalPolicies. Shorthand `egress.from.*` and
// `ingress.to.*` are deferred — those fields use patternProperties in the
// JSON schema and currently land as `map[string]apiextensionsv1.JSON` in
// the generated Go types; decoding them needs follow-up work.
func generateNetworkPolicies(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio) ([]client.Object, error) {
	var out []client.Object

	out = append(out, defaultEgressPolicies(pkg, spec, istio)...)
	out = append(out, defaultIngressPolicies(pkg, spec)...)

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

func defaultEgressPolicies(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio) []client.Object {
	prepend := spec.PrependReleaseName
	npLabels := defaultNetpolLabels("egress")
	var out []client.Object

	if emitDefault() {
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
	if emitDefault() {
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
	if emitDefault() {
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
	if emitDefault() && !istioAmbient(istio) {
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

	if emitDefault() {
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
	if emitDefault() {
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
	if emitDefault() {
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

// emitDefault returns whether to emit a default policy. v1 always emits all
// defaults when networkPolicies.enabled is true; per-default disabling needs
// the schema-to-go script to emit *bool for these fields (today they're plain
// bool, so unset == false and we can't tell intent).
func emitDefault() bool {
	return true
}

func defaultNetpolLabels(direction string) map[string]string {
	return map[string]string{
		LabelNetpolSource:                            LabelNetpolSourceValue,
		"network-policies.bigbang.dev/direction":     direction,
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
