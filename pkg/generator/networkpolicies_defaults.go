package generator

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// The 8 default NetworkPolicies and their per-default enablement gates,
// mirroring bb-common's {egress,ingress}/defaults templates.

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

func defaultIngressPolicies(pkg *bbv1alpha1.Package, spec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio) []client.Object {
	prepend := spec.PrependReleaseName
	npLabels := defaultNetpolLabels("ingress")
	ambient := istioAmbient(istio)
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
	// allow-prometheus-to-istio-sidecar is sidecar-only — bb-common
	// suppresses it under ambient mode (no per-pod sidecar = no port 15020).
	if ingressAllowPromToSidecar(spec) && !ambient {
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
	// allow-ambient-kubelet: under ambient mode, kubelet probes pods from
	// the node's link-local 169.254.7.127. Matches bb-common.
	if ambient && ingressSideEnabled(spec) {
		out = append(out, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:   prependName(prepend, pkg.Name, "default-ingress-allow-ambient-kubelet"),
				Labels: cloneLabels(npLabels),
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{{
						IPBlock: &networkingv1.IPBlock{CIDR: "169.254.7.127/32"},
					}},
				}},
			},
		})
	}
	return out
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

func istioAmbient(istio *bbv1alpha1.Istio) bool {
	return istio != nil && istio.Ambient != nil && istio.Ambient.Enabled
}
