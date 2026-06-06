package generator

import (
	"strings"

	istiosecv1beta1 "istio.io/api/security/v1beta1"
	istiotypev1beta1 "istio.io/api/type/v1beta1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// generateIstio renders the istio resource set for the package.
//
// v1 scope: PeerAuthentication only. Sidecar, default
// AuthorizationPolicies, and custom passthrough are deferred.
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

	return out, nil
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
