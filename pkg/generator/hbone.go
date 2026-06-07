package generator

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

const (
	hbonePort       = 15008
	hboneLabel      = "ambient.istio.network-policies.bigbang.dev/hbone-injected"
	hboneLabelValue = "true"
)

// hboneEnabled mirrors bb-common's effective-values helper. Ambient mode
// auto-enables port-15008 injection; the explicit flag is a manual override
// for non-ambient packages that talk to ambient peers.
func hboneEnabled(spec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio) bool {
	if istioAmbient(istio) {
		return true
	}
	if spec == nil || spec.HbonePortInjection == nil {
		return false
	}
	return spec.HbonePortInjection.Enabled
}

// injectHBonePorts adds TCP/15008 to every egress/ingress rule that already
// has explicit ports AND at least one k8s-style peer (namespaceSelector or
// podSelector). Rules whose peers are purely ipBlock are skipped — HBONE is
// a mesh-internal protocol. Injected NetworkPolicies are labeled so
// operators can audit what was mutated.
func injectHBonePorts(objs []client.Object) {
	for _, o := range objs {
		np, ok := o.(*networkingv1.NetworkPolicy)
		if !ok {
			continue
		}
		mutated := false
		for i := range np.Spec.Egress {
			if injectIntoPorts(&np.Spec.Egress[i].Ports, peerHasK8sSelector(np.Spec.Egress[i].To)) {
				mutated = true
			}
		}
		for i := range np.Spec.Ingress {
			if injectIntoPorts(&np.Spec.Ingress[i].Ports, peerHasK8sSelector(np.Spec.Ingress[i].From)) {
				mutated = true
			}
		}
		if mutated {
			if np.Labels == nil {
				np.Labels = map[string]string{}
			}
			np.Labels[hboneLabel] = hboneLabelValue
		}
	}
}

func peerHasK8sSelector(peers []networkingv1.NetworkPolicyPeer) bool {
	for _, p := range peers {
		if p.NamespaceSelector != nil || p.PodSelector != nil {
			return true
		}
	}
	return false
}

// injectIntoPorts returns true if it added the hbone port. Rules without
// any ports are left alone (matches bb-common: those rules are wide open
// already, no need to inject).
func injectIntoPorts(ports *[]networkingv1.NetworkPolicyPort, eligible bool) bool {
	if !eligible || len(*ports) == 0 {
		return false
	}
	tcp := corev1.ProtocolTCP
	target := intstr.FromInt(hbonePort)
	for _, p := range *ports {
		if p.Protocol != nil && *p.Protocol == tcp && p.Port != nil && p.Port.Type == intstr.Int && p.Port.IntValue() == hbonePort {
			return false
		}
	}
	*ports = append(*ports, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &target})
	return true
}
