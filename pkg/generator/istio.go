package generator

import (
	"encoding/json"
	"fmt"
	"strings"

	istionetv1alpha3 "istio.io/api/networking/v1alpha3"
	istiosecv1beta1 "istio.io/api/security/v1beta1"
	istiotypev1beta1 "istio.io/api/type/v1beta1"
	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// generateIstio renders the istio resource set for the package.
//
// Emits PeerAuthentication and (when sidecar.enabled, ambient off) a
// namespace-wide Sidecar. Default/generated AuthorizationPolicies live in
// their own pass (see authorizationpolicies.go).
func generateIstio(pkg *bbv1alpha1.Package, spec *bbv1alpha1.Istio) ([]client.Object, error) {
	var out []client.Object

	mode := mTLSMode(spec)
	out = append(out, &istiosecv1.PeerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name: prependName(spec.PrependReleaseName, pkg.Name, "default-peer-auth"),
		},
		Spec: istiosecv1beta1.PeerAuthentication{
			Mtls: &istiosecv1beta1.PeerAuthentication_MutualTLS{Mode: mode},
		},
	})

	if sc := sidecarResource(pkg, spec); sc != nil {
		out = append(out, sc)
	}

	ses, err := generateCustomServiceEntries(spec)
	if err != nil {
		return nil, fmt.Errorf("istio.serviceEntries: %w", err)
	}
	out = append(out, ses...)

	return out, nil
}

// generateCustomServiceEntries emits one ServiceEntry per entry in
// `istio.serviceEntries.custom[]`. The user-provided `spec` is a raw JSON
// map (since the schema can't statically describe every Istio API field),
// so we round-trip through JSON to populate the typed proto.
func generateCustomServiceEntries(spec *bbv1alpha1.Istio) ([]client.Object, error) {
	if spec == nil || spec.ServiceEntries == nil {
		return nil, nil
	}
	var out []client.Object
	for i, elem := range spec.ServiceEntries.Custom {
		if elem.Name == "" {
			return nil, fmt.Errorf("custom[%d]: name is required", i)
		}
		se := &istionetv1.ServiceEntry{
			ObjectMeta: metav1.ObjectMeta{
				Name:        elem.Name,
				Labels:      map[string]string(elem.Labels),
				Annotations: map[string]string(elem.Annotations),
			},
		}
		if len(elem.Spec) > 0 {
			b, err := json.Marshal(elem.Spec)
			if err != nil {
				return nil, fmt.Errorf("custom[%d] %q: %w", i, elem.Name, err)
			}
			if err := json.Unmarshal(b, &se.Spec); err != nil {
				return nil, fmt.Errorf("custom[%d] %q: %w", i, elem.Name, err)
			}
		}
		out = append(out, se)
	}
	return out, nil
}

// sidecarResource returns the namespace-wide Sidecar, or nil when the
// feature is off. Ambient mode suppresses the Sidecar (matches bb-common's
// bb-common.istio.sidecar.enabled helper).
func sidecarResource(pkg *bbv1alpha1.Package, spec *bbv1alpha1.Istio) *istionetv1.Sidecar {
	if spec == nil || spec.Sidecar == nil || !spec.Sidecar.Enabled {
		return nil
	}
	if spec.Ambient != nil && spec.Ambient.Enabled {
		return nil
	}
	mode := istionetv1alpha3.OutboundTrafficPolicy_REGISTRY_ONLY
	if strings.ToUpper(string(spec.Sidecar.OutboundTrafficPolicyMode)) == "ALLOW_ANY" {
		mode = istionetv1alpha3.OutboundTrafficPolicy_ALLOW_ANY
	}
	return &istionetv1.Sidecar{
		ObjectMeta: metav1.ObjectMeta{
			Name: prependName(spec.PrependReleaseName, pkg.Name, "sidecar"),
		},
		Spec: istionetv1alpha3.Sidecar{
			OutboundTrafficPolicy: &istionetv1alpha3.OutboundTrafficPolicy{Mode: mode},
		},
	}
}

func mTLSMode(spec *bbv1alpha1.Istio) istiosecv1beta1.PeerAuthentication_MutualTLS_Mode {
	if spec == nil || spec.Mtls == nil || spec.Mtls.Mode == "" {
		return istiosecv1beta1.PeerAuthentication_MutualTLS_STRICT
	}
	switch strings.ToUpper(string(spec.Mtls.Mode)) {
	case "STRICT":
		return istiosecv1beta1.PeerAuthentication_MutualTLS_STRICT
	case "PERMISSIVE":
		return istiosecv1beta1.PeerAuthentication_MutualTLS_PERMISSIVE
	case "DISABLE":
		return istiosecv1beta1.PeerAuthentication_MutualTLS_DISABLE
	case "UNSET":
		return istiosecv1beta1.PeerAuthentication_MutualTLS_UNSET
	default:
		return istiosecv1beta1.PeerAuthentication_MutualTLS_STRICT
	}
}

// prependName mirrors bb-common's `prependReleaseName` behavior: when true,
// emitted names get the package name prefixed (e.g. "myapp-default-peer-auth").
func prependName(prepend bool, releaseName, name string) string {
	if !prepend {
		return name
	}
	return releaseName + "-" + name
}

// (Unused import guard to keep the istio type imported even if codepaths
// elsewhere later reference it through other identifiers.)
var _ = istiotypev1beta1.WorkloadSelector{}
