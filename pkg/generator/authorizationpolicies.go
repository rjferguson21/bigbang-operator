package generator

import (
	"encoding/json"
	"fmt"
	"strconv"

	istiosecv1beta1 "istio.io/api/security/v1beta1"
	istiotypev1beta1 "istio.io/api/type/v1beta1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// authzEnabled returns true when the operator should emit
// AuthorizationPolicies — either `istio.authorizationPolicies.enabled` is
// true, OR `istio.ambient.enabled` is true (which forces them on per
// plan/istio.md).
func authzEnabled(istio *bbv1alpha1.Istio) bool {
	if istio == nil {
		return false
	}
	if istio.Ambient != nil && istio.Ambient.Enabled {
		return true
	}
	if istio.AuthorizationPolicies != nil && istio.AuthorizationPolicies.Enabled {
		return true
	}
	return false
}

func generateFromNetpol(istio *bbv1alpha1.Istio) bool {
	if istio == nil || istio.AuthorizationPolicies == nil {
		return true // bb-common default
	}
	if istio.AuthorizationPolicies.GenerateFromNetpol == nil {
		return true
	}
	return *istio.AuthorizationPolicies.GenerateFromNetpol
}

func authzDefaultsEnabled(istio *bbv1alpha1.Istio) bool {
	if istio == nil || istio.AuthorizationPolicies == nil || istio.AuthorizationPolicies.Defaults == nil {
		return true
	}
	return defaultEnabled(istio.AuthorizationPolicies.Defaults.Enabled)
}

func authzAllowNothing(istio *bbv1alpha1.Istio) bool {
	if !authzDefaultsEnabled(istio) {
		return false
	}
	d := istio.AuthorizationPolicies.Defaults
	if d == nil || d.DenyAll == nil {
		return true
	}
	return defaultEnabled(d.DenyAll.Enabled)
}

func authzAllowAllInNS(istio *bbv1alpha1.Istio) bool {
	if !authzDefaultsEnabled(istio) {
		return false
	}
	d := istio.AuthorizationPolicies.Defaults
	if d == nil || d.AllowInNamespace == nil {
		return true
	}
	return defaultEnabled(d.AllowInNamespace.Enabled)
}

// generateDefaultAuthzPolicies emits the two baseline AuthorizationPolicies
// (`default-authz-allow-nothing` and `default-authz-allow-all-in-ns`).
// Both are also gated on ingress netpol defaults — if ingress defaults are
// disabled, neither AP is emitted (matches bb-common).
func generateDefaultAuthzPolicies(pkg *bbv1alpha1.Package, istio *bbv1alpha1.Istio, npSpec *bbv1alpha1.NetworkPolicies) []client.Object {
	if !authzEnabled(istio) {
		return nil
	}
	// Per plan/istio.md: default APs are also gated on
	// `networkPolicies.ingress.defaults.enabled`.
	if npSpec != nil && !ingressSideEnabled(npSpec) {
		return nil
	}

	var out []client.Object
	if authzAllowNothing(istio) {
		out = append(out, &istiosecv1.AuthorizationPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default-authz-allow-nothing",
			},
			Spec: istiosecv1beta1.AuthorizationPolicy{},
		})
	}
	if authzAllowAllInNS(istio) {
		out = append(out, &istiosecv1.AuthorizationPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default-authz-allow-all-in-ns",
			},
			Spec: istiosecv1beta1.AuthorizationPolicy{
				Action: istiosecv1beta1.AuthorizationPolicy_ALLOW,
				Rules: []*istiosecv1beta1.Rule{{
					From: []*istiosecv1beta1.Rule_From{{
						Source: &istiosecv1beta1.Source{
							Namespaces: []string{pkg.Namespace},
						},
					}},
				}},
			},
		})
	}
	return out
}

// generateAuthzFromIngressShorthand emits one AuthorizationPolicy per
// shorthand ingress entry, mirroring bb-common's
// `generateFromNetpol: true` behavior. Each AP allows traffic to the
// destination pod from the source namespace (or principal, when the remote
// key carries an `<identity>@` prefix).
func generateAuthzFromIngressShorthand(pkg *bbv1alpha1.Package, npSpec *bbv1alpha1.NetworkPolicies, istio *bbv1alpha1.Istio) ([]client.Object, error) {
	if !authzEnabled(istio) || !generateFromNetpol(istio) {
		return nil, nil
	}
	if npSpec == nil || npSpec.Ingress == nil {
		return nil, nil
	}
	prepend := npSpec.PrependReleaseName

	var out []client.Object
	for _, localKey := range sortedKeys(npSpec.Ingress.To) {
		var local shorthandSource
		if err := json.Unmarshal(npSpec.Ingress.To[localKey].Raw, &local); err != nil {
			return nil, fmt.Errorf("authz from ingress.to.%s: %w", localKey, err)
		}
		if local.From == nil {
			continue
		}
		parsedLocal, err := parseIngressLocalKey(localKey)
		if err != nil {
			return nil, fmt.Errorf("authz ingress.to: %w", err)
		}
		for _, remoteKey := range sortedKeys(local.From.K8s) {
			target := local.From.K8s[remoteKey]
			if !target.Enabled {
				continue
			}
			remote, err := parseIngressRemoteKey(remoteKey)
			if err != nil {
				return nil, fmt.Errorf("authz ingress.to.%s.from.k8s: %w", localKey, err)
			}
			ap := buildAuthzFromShorthand(pkg, prepend, parsedLocal, local, remoteKey, remote)
			out = append(out, ap)
		}
	}
	return out, nil
}

func buildAuthzFromShorthand(pkg *bbv1alpha1.Package, prepend bool, local *parsedLocalIngressKey, srcEntry shorthandSource, remoteKey string, remote *parsedK8sRemote) *istiosecv1.AuthorizationPolicy {
	// Mirror bb-common's naming: NP name with everything from "-from" on
	// stripped, then "-from-ns-<ns>" or "-from-ns-<ns>-with-identity-<sa>".
	netpolName := fmt.Sprintf("allow-ingress-to-%s", local.Pod)
	if local.Protocol != "" && local.Protocol != "TCP" {
		netpolName += "-" + lowercase(local.Protocol)
	}
	if len(local.Ports) > 0 {
		netpolName += "-" + lowercase(local.Protocol)
	}
	netpolName += "-" + namePortSuffix(local.Ports, local.HasPortRange)

	var name string
	annotations := map[string]string{}
	if remote.Identity != "" {
		name = fmt.Sprintf("%s-from-ns-%s-with-identity-%s", netpolName, remote.Namespace, remote.Identity)
		annotations["generated.authorization-policies.bigbang.dev/from-spiffe"] = remoteKey
		annotations["generated.authorization-policies.bigbang.dev/identity"] = remote.Identity
	} else if remote.Namespace == "*" {
		name = netpolName + "-from-ns-any"
		annotations["generated.authorization-policies.bigbang.dev/from-namespace"] = remoteKey
	} else {
		name = netpolName + "-from-ns-" + remote.Namespace
		annotations["generated.authorization-policies.bigbang.dev/from-namespace"] = remoteKey
		if remote.Pod != "" {
			annotations["generated.authorization-policies.bigbang.dev/pod"] = remote.Pod
		}
	}
	name = prependName(prepend, pkg.Name, name)

	selector := &istiotypev1beta1.WorkloadSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": local.Pod}}
	if len(srcEntry.PodSelector) > 0 {
		selector = &istiotypev1beta1.WorkloadSelector{MatchLabels: srcEntry.PodSelector}
	}

	from := &istiosecv1beta1.Rule_From{Source: &istiosecv1beta1.Source{}}
	if remote.Identity != "" {
		from.Source.Principals = []string{fmt.Sprintf("cluster.local/ns/%s/sa/%s", remote.Namespace, remote.Identity)}
	} else {
		from.Source.Namespaces = []string{remote.Namespace}
	}

	rule := &istiosecv1beta1.Rule{From: []*istiosecv1beta1.Rule_From{from}}
	if len(local.Ports) > 0 {
		op := &istiosecv1beta1.Operation{Ports: make([]string, 0, len(local.Ports))}
		if local.HasPortRange && len(local.Ports) == 2 {
			for p := local.Ports[0]; p <= local.Ports[1]; p++ {
				op.Ports = append(op.Ports, strconv.Itoa(p))
			}
		} else {
			for _, p := range local.Ports {
				op.Ports = append(op.Ports, strconv.Itoa(p))
			}
		}
		rule.To = []*istiosecv1beta1.Rule_To{{Operation: op}}
	}

	return &istiosecv1.AuthorizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
		},
		Spec: istiosecv1beta1.AuthorizationPolicy{
			Selector: selector,
			Action:   istiosecv1beta1.AuthorizationPolicy_ALLOW,
			Rules:    []*istiosecv1beta1.Rule{rule},
		},
	}
}

// generateAuthzFromRoutes emits one AuthorizationPolicy per inbound route
// per gateway, restricting callers to the gateway's ServiceAccount. Gated on
// `istio.authorizationPolicies.enabled` (or `istio.ambient.enabled`).
// Independent of `generateFromNetpol` — that flag only governs the shorthand
// fan-out, not per-route policies.
func generateAuthzFromRoutes(pkg *bbv1alpha1.Package, routes *bbv1alpha1.Routes, istio *bbv1alpha1.Istio) ([]client.Object, error) {
	if !authzEnabled(istio) || routes == nil {
		return nil, nil
	}

	var out []client.Object
	for _, name := range sortedKeys(routes.Inbound) {
		route, err := decodeInbound(routes.Inbound[name])
		if err != nil {
			return nil, fmt.Errorf("authz routes.inbound.%s: %w", name, err)
		}
		if !route.Enabled {
			continue
		}
		for _, gw := range route.Gateways {
			gwNS, gwName, ok := splitGateway(gw)
			if !ok {
				continue
			}
			out = append(out, buildAuthzFromRoute(route, gwNS, gwName))
		}
	}
	return out, nil
}

func buildAuthzFromRoute(route *bbv1alpha1.InboundRoute, gwNS, gwName string) *istiosecv1.AuthorizationPolicy {
	apName := fmt.Sprintf("%s-%s-authz-policy", serviceLeaf(route.Service), gwName)

	rule := &istiosecv1beta1.Rule{
		From: []*istiosecv1beta1.Rule_From{{
			Source: &istiosecv1beta1.Source{
				Principals: []string{fmt.Sprintf(
					"cluster.local/ns/%s/sa/%s-ingressgateway-service-account",
					gwNS, gwName)},
			},
		}},
	}
	if p := route.Port; p != nil {
		if n := portNumber(p); n != 0 {
			rule.To = []*istiosecv1beta1.Rule_To{{
				Operation: &istiosecv1beta1.Operation{
					Ports: []string{strconv.FormatUint(uint64(n), 10)},
				},
			}}
		}
	}

	return &istiosecv1.AuthorizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        apName,
			Labels:      mergeMaps(route.Labels, nil),
			Annotations: mergeMaps(route.Annotations, nil),
		},
		Spec: istiosecv1beta1.AuthorizationPolicy{
			Selector: &istiotypev1beta1.WorkloadSelector{MatchLabels: route.Selector},
			Action:   istiosecv1beta1.AuthorizationPolicy_ALLOW,
			Rules:    []*istiosecv1beta1.Rule{rule},
		},
	}
}

func lowercase(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}
