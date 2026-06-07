package generator

import (
	"encoding/json"
	"fmt"
	"strconv"

	istiosecv1beta1 "istio.io/api/security/v1beta1"
	istiotypev1beta1 "istio.io/api/type/v1beta1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
		for _, cidrKey := range sortedKeys(local.From.Cidr) {
			if !local.From.Cidr[cidrKey].Enabled {
				continue
			}
			cidr, err := parseIngressCIDRKey(cidrKey)
			if err != nil {
				return nil, fmt.Errorf("authz ingress.to.%s.from.cidr: %w", localKey, err)
			}
			ap := buildAuthzFromCIDR(pkg, prepend, parsedLocal, local, cidrKey, cidr)
			out = append(out, ap)
		}
	}
	return out, nil
}

// buildAuthzFromCIDR mirrors the K8s shorthand counterpart but emits an AP
// whose source is `ipBlocks: [<cidr>]`. The name reuses the companion
// NetworkPolicy's name (bb-common does this so the AP and NP are easy to
// correlate in dashboards). When the local key has ports, they translate
// into the AP's `to.operation.ports`.
func buildAuthzFromCIDR(pkg *bbv1alpha1.Package, prepend bool, local *parsedLocalIngressKey, srcEntry shorthandSource, cidrKey string, cidr *parsedCIDR) *istiosecv1.AuthorizationPolicy {
	// Reuse the NP naming scheme (kept in sync with buildIngressCIDRNetpol).
	name := fmt.Sprintf("allow-ingress-to-%s", local.Pod)
	if local.Protocol != "" && local.Protocol != "TCP" {
		name += "-" + lowercase(local.Protocol)
	}
	if len(local.Ports) > 0 {
		name += "-" + lowercase(local.Protocol) + "-" + namePortSuffix(local.Ports, local.HasPortRange)
	}
	if cidr.CIDR == "0.0.0.0/0" {
		name += "-from-anywhere"
	} else {
		name += "-from-cidr-" + cidrNameSegment(cidr.CIDR)
	}
	name = prependName(prepend, pkg.Name, name)

	selector := &istiotypev1beta1.WorkloadSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": local.Pod}}
	if len(srcEntry.PodSelector) > 0 {
		selector = &istiotypev1beta1.WorkloadSelector{MatchLabels: srcEntry.PodSelector}
	}

	rule := &istiosecv1beta1.Rule{
		From: []*istiosecv1beta1.Rule_From{{
			Source: &istiosecv1beta1.Source{IpBlocks: []string{cidr.CIDR}},
		}},
	}
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
			Name: name,
			Annotations: map[string]string{
				"generated.authorization-policies.bigbang.dev/from-cidr": cidrKey,
				"generated.authorization-policies.bigbang.dev/cidr":      cidr.CIDR,
			},
		},
		Spec: istiosecv1beta1.AuthorizationPolicy{
			Selector: selector,
			Action:   istiosecv1beta1.AuthorizationPolicy_ALLOW,
			Rules:    []*istiosecv1beta1.Rule{rule},
		},
	}
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
			out = append(out, buildAuthzFromRoute(pkg, routes.PrependReleaseName, route, gwNS, gwName))
		}
	}
	return out, nil
}

func buildAuthzFromRoute(pkg *bbv1alpha1.Package, prepend bool, route *bbv1alpha1.InboundRoute, gwNS, gwName string) *istiosecv1.AuthorizationPolicy {
	apName := prependName(prepend, pkg.Name, fmt.Sprintf("%s-%s-authz-policy", serviceLeaf(route.Service), gwName))

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

// generateCustomAuthzPolicies emits one AuthorizationPolicy per entry in
// `istio.authorizationPolicies.custom[]` (list form) and per ENABLED entry
// in `istio.authorizationPolicies.additionalPolicies` (map form, gated by
// the per-entry `enabled` field). Both forms accept raw JSON specs because
// the AP API is large and the schema only loosely types `spec`.
//
// Unlike the generated APs, these are not gated on
// `authorizationPolicies.enabled`: users opting in by declaring a custom AP
// have already expressed intent. They are gated on `istio.enabled` by the
// caller (Generate only invokes generateIstio when istio is on, but this
// runs in the AP pass — so we also short-circuit when istio is nil/off).
func generateCustomAuthzPolicies(istio *bbv1alpha1.Istio) ([]client.Object, error) {
	if istio == nil || !istio.Enabled || istio.AuthorizationPolicies == nil {
		return nil, nil
	}
	apSpec := istio.AuthorizationPolicies

	var out []client.Object
	for i, elem := range apSpec.Custom {
		if elem.Name == "" {
			return nil, fmt.Errorf("custom[%d]: name is required", i)
		}
		ap, err := buildRawAuthzPolicy(elem.Name, map[string]string(elem.Labels), map[string]string(elem.Annotations), elem.Spec)
		if err != nil {
			return nil, fmt.Errorf("custom[%d] %q: %w", i, elem.Name, err)
		}
		out = append(out, ap)
	}
	for _, key := range sortedKeys(apSpec.AdditionalPolicies) {
		elem := apSpec.AdditionalPolicies[key]
		if !elem.Enabled {
			continue
		}
		name := key
		if elem.Name != nil && *elem.Name != "" {
			name = *elem.Name
		}
		// elem.Namespace is intentionally ignored — owner refs across
		// namespaces aren't supported, so all emitted resources live in the
		// Package's namespace (stamped in Generate).
		ap, err := buildAdditionalAuthzPolicy(name, map[string]string(elem.Labels), map[string]string(elem.Annotations), elem.Spec)
		if err != nil {
			return nil, fmt.Errorf("additionalPolicies.%s: %w", key, err)
		}
		out = append(out, ap)
	}
	return out, nil
}

// buildRawAuthzPolicy materializes one AP from a raw spec map. The map's
// shape is opaque to the operator — round-trip through JSON populates the
// typed proto so SSA marshals it correctly.
func buildRawAuthzPolicy(name string, labels, annotations map[string]string, rawSpec map[string]apiextensionsv1.JSON) (*istiosecv1.AuthorizationPolicy, error) {
	ap := &istiosecv1.AuthorizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
	}
	if len(rawSpec) == 0 {
		return ap, nil
	}
	b, err := json.Marshal(rawSpec)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &ap.Spec); err != nil {
		return nil, err
	}
	return ap, nil
}

// buildAdditionalAuthzPolicy is the same as buildRawAuthzPolicy but takes
// the AdditionalPolicies typed spec (action + rules + selector). The
// schema models these fields more tightly than the loose `custom[]`
// passthrough, but the spec round-trip is the same shape.
func buildAdditionalAuthzPolicy(name string, labels, annotations map[string]string, spec bbv1alpha1.IstioAuthorizationPoliciesAdditionalPoliciesValueSpec) (*istiosecv1.AuthorizationPolicy, error) {
	ap := &istiosecv1.AuthorizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &ap.Spec); err != nil {
		return nil, err
	}
	return ap, nil
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
