package generator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// protoTCP is the default shorthand protocol; appears in many spots both as
// the initialized field and as a "skip-when-default" check on naming.
const protoTCP = "TCP"

// parsedK8sRemote represents one parsed value from
// `egress.from.<src>.to.k8s.<key>` or `ingress.to.<dst>.from.k8s.<key>`.
//
// For egress, key format: `[<tcp|udp>://]<ns>[/<pod>][:<ports>]`
// For ingress, key format: `[<identity>@]<ns>[/<pod>]` (no ports, those
// live on the *local* key).
type parsedK8sRemote struct {
	// Namespace is the empty string when the source key used "*".
	Namespace string
	// Pod is the empty string when missing or "*".
	Pod string
	// Identity is only set on ingress remote keys (the ServiceAccount
	// before the `@`). Unused for NetworkPolicy generation; reserved for
	// future AuthorizationPolicy generation.
	Identity string
	// Protocol is "TCP" by default, or "UDP"/"TCP" when an explicit
	// `udp://` or `tcp://` prefix is present (egress only).
	Protocol string
	// Ports as parsed from `:<port>` / `:<a>-<b>` / `:[a,b,c]`.
	Ports []int
	// HasPortRange is true when the spec was `<a>-<b>`.
	HasPortRange bool
}

// parsedLocalIngressKey represents one parsed value from
// `ingress.to.<key>`. Format: `[<tcp|udp>://]<pod-name>[:<ports>]`.
type parsedLocalIngressKey struct {
	Pod          string
	Protocol     string
	Ports        []int
	HasPortRange bool
}

var (
	egressRemoteKeyRe = regexp.MustCompile(
		`^((tcp|udp)://)?([A-Za-z0-9-]+|\*)(/([A-Za-z0-9-]+|\*))?(:(\d+|\d+-\d+|\[?\d+(,\d+)*\]?))?$`)
	ingressRemoteKeyRe = regexp.MustCompile(
		`^([A-Za-z0-9-]+@)?([A-Za-z0-9-]+|\*)(/([A-Za-z0-9-]+|\*))?$`)
	ingressLocalKeyRe = regexp.MustCompile(
		`^((tcp|udp)://)?[\w-]+(:(\[?\d+(,\d+)*\]?|\d+|\d+-\d+))?$`)
	egressCIDRKeyRe = regexp.MustCompile(
		`^((tcp|udp)://)?(\d+\.){3}\d+/\d+(:(\d+|\d+-\d+|\[?\d+(,\d+)*\]?))?$`)
	ingressCIDRKeyRe = regexp.MustCompile(
		`^(\d+\.){3}\d+/\d+$`)
)

func parseEgressRemoteKey(key string) (*parsedK8sRemote, error) {
	if !egressRemoteKeyRe.MatchString(key) {
		return nil, fmt.Errorf("egress k8s key %q does not match `[<tcp|udp>://]<ns>[/<pod>][:<ports>]`", key)
	}
	r := &parsedK8sRemote{Protocol: protoTCP}
	rest := key
	if i := strings.Index(rest, "://"); i >= 0 {
		r.Protocol = strings.ToUpper(rest[:i])
		rest = rest[i+3:]
	}
	if i := strings.Index(rest, ":"); i >= 0 {
		ports, hasRange, err := parsePortSpec(rest[i+1:])
		if err != nil {
			return nil, err
		}
		r.Ports = ports
		r.HasPortRange = hasRange
		rest = rest[:i]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		r.Namespace = rest[:i]
		r.Pod = rest[i+1:]
	} else {
		r.Namespace = rest
	}
	return r, nil
}

func parseIngressRemoteKey(key string) (*parsedK8sRemote, error) {
	if !ingressRemoteKeyRe.MatchString(key) {
		return nil, fmt.Errorf("ingress k8s key %q does not match `[<identity>@]<ns>[/<pod>]`", key)
	}
	r := &parsedK8sRemote{Protocol: protoTCP}
	rest := key
	if i := strings.Index(rest, "@"); i >= 0 {
		r.Identity = rest[:i]
		rest = rest[i+1:]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		r.Namespace = rest[:i]
		r.Pod = rest[i+1:]
	} else {
		r.Namespace = rest
	}
	return r, nil
}

func parseIngressLocalKey(key string) (*parsedLocalIngressKey, error) {
	if !ingressLocalKeyRe.MatchString(key) {
		return nil, fmt.Errorf("ingress local key %q does not match `[<udp|tcp>://]<pod-name>[:<ports>]`", key)
	}
	r := &parsedLocalIngressKey{Protocol: protoTCP}
	rest := key
	if i := strings.Index(rest, "://"); i >= 0 {
		r.Protocol = strings.ToUpper(rest[:i])
		rest = rest[i+3:]
	}
	if i := strings.Index(rest, ":"); i >= 0 {
		ports, hasRange, err := parsePortSpec(rest[i+1:])
		if err != nil {
			return nil, err
		}
		r.Ports = ports
		r.HasPortRange = hasRange
		rest = rest[:i]
	}
	r.Pod = rest
	return r, nil
}

// parsedCIDR represents one parsed value from
// `egress.from.<src>.to.cidr.<key>` or `ingress.to.<dst>.from.cidr.<key>`.
//
// Egress key format: `[<tcp|udp>://]<cidr>[:<ports>]`.
// Ingress key format: `<cidr>` (ports live on the local ingress key).
type parsedCIDR struct {
	CIDR         string
	Protocol     string
	Ports        []int
	HasPortRange bool
}

func parseEgressCIDRKey(key string) (*parsedCIDR, error) {
	if !egressCIDRKeyRe.MatchString(key) {
		return nil, fmt.Errorf("egress cidr key %q does not match `[<tcp|udp>://]<cidr>[:<ports>]`", key)
	}
	r := &parsedCIDR{Protocol: protoTCP}
	rest := key
	if i := strings.Index(rest, "://"); i >= 0 {
		r.Protocol = strings.ToUpper(rest[:i])
		rest = rest[i+3:]
	}
	// CIDR contains a "/"; ports separator is ":" *after* the CIDR.
	// Find the last ":" — safe because CIDR has no ":" in IPv4.
	if i := strings.LastIndex(rest, ":"); i > strings.LastIndex(rest, "/") {
		ports, hasRange, err := parsePortSpec(rest[i+1:])
		if err != nil {
			return nil, err
		}
		r.Ports = ports
		r.HasPortRange = hasRange
		rest = rest[:i]
	}
	r.CIDR = rest
	return r, nil
}

func parseIngressCIDRKey(key string) (*parsedCIDR, error) {
	if !ingressCIDRKeyRe.MatchString(key) {
		return nil, fmt.Errorf("ingress cidr key %q does not match `<cidr>`", key)
	}
	return &parsedCIDR{CIDR: key, Protocol: protoTCP}, nil
}

// parsePortSpec accepts `<n>`, `<a>-<b>`, or `<a>,<b>,...` (optionally
// wrapped in `[]`). Returns the port list and whether it was a range.
func parsePortSpec(s string) ([]int, bool, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if i := strings.Index(s, "-"); i >= 0 {
		a, err1 := strconv.Atoi(s[:i])
		b, err2 := strconv.Atoi(s[i+1:])
		if err1 != nil || err2 != nil {
			return nil, false, fmt.Errorf("bad port range %q", s)
		}
		return []int{a, b}, true, nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, false, fmt.Errorf("bad port %q", p)
		}
		out = append(out, n)
	}
	return out, false, nil
}

// --- shorthand value decoding -------------------------------------------
//
// The key grammar above parses map *keys*; the types below decode the map
// *values* (`true`, `{enabled, podSelector, metadata, ...}`, literal specs).

// shorthandSource is one decoded `egress.from.<src>` or `ingress.to.<dst>`
// outer-map value. The inner `to.k8s` / `from.k8s` maps use the same
// pattern: `<key> -> true | { enabled, podSelector?, namespaceSelector? }`.
//
// The custom unmarshaler tolerates two podSelector shapes (matches what
// bb-common accepts):
//   - flat:   podSelector: {app: foo}
//   - nested: podSelector: {matchLabels: {app: foo}}
type shorthandSource struct {
	PodSelector map[string]string  `json:"-"`
	Metadata    *shorthandMetadata `json:"metadata,omitempty"`
	To          *shorthandPeer     `json:"to,omitempty"`   // egress
	From        *shorthandPeer     `json:"from,omitempty"` // ingress
}

func (s *shorthandSource) UnmarshalJSON(b []byte) error {
	var raw struct {
		PodSelector map[string]interface{} `json:"podSelector,omitempty"`
		Metadata    *shorthandMetadata     `json:"metadata,omitempty"`
		To          *shorthandPeer         `json:"to,omitempty"`
		From        *shorthandPeer         `json:"from,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	s.PodSelector = flattenMatchLabels(raw.PodSelector)
	s.Metadata = raw.Metadata
	s.To = raw.To
	s.From = raw.From
	return nil
}

// shorthandMetadata is the `metadata: {labels, annotations}` block accepted
// at both the local (pod) and remote (rule) level. Remote wins over local on
// key conflicts; generated labels/annotations win over both.
type shorthandMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// mergeShorthandMetadata mirrors bb-common's metadata-overrides helper:
// remote keys override local ones.
func mergeShorthandMetadata(local, remote *shorthandMetadata) *shorthandMetadata {
	if local == nil && remote == nil {
		return nil
	}
	out := &shorthandMetadata{}
	if local != nil {
		out.Labels = mergeMaps(out.Labels, local.Labels)
		out.Annotations = mergeMaps(out.Annotations, local.Annotations)
	}
	if remote != nil {
		out.Labels = mergeMaps(out.Labels, remote.Labels)
		out.Annotations = mergeMaps(out.Annotations, remote.Annotations)
	}
	return out
}

// applyShorthandMetadata folds user-supplied labels/annotations into an
// object's metadata. Generated keys take precedence, matching bb-common
// (where `merge $netpol $userMetadata` keeps the netpol's own keys).
func applyShorthandMetadata(obj metav1.Object, meta *shorthandMetadata) {
	if meta == nil {
		return
	}
	if len(meta.Labels) > 0 {
		obj.SetLabels(mergeMaps(meta.Labels, obj.GetLabels()))
	}
	if len(meta.Annotations) > 0 {
		obj.SetAnnotations(mergeMaps(meta.Annotations, obj.GetAnnotations()))
	}
}

type shorthandPeer struct {
	K8s        map[string]shorthandTarget `json:"k8s,omitempty"`
	Definition map[string]shorthandTarget `json:"definition,omitempty"`
	Cidr       map[string]shorthandTarget `json:"cidr,omitempty"`
	Literal    map[string]literalTarget   `json:"literal,omitempty"`
}

// literalTarget is one `to.literal.<key>` / `from.literal.<key>` entry: a
// raw egress/ingress rule array emitted as-is, mirroring bb-common's
// from-spec-literal generator. Note literal rules bypass excludeCIDRs.
type literalTarget struct {
	Enabled  bool               `json:"-"`
	Metadata *shorthandMetadata `json:"-"`
	Spec     json.RawMessage    `json:"-"`
}

func (t *literalTarget) UnmarshalJSON(b []byte) error {
	if len(b) == 4 && string(b) == "true" {
		t.Enabled = true
		return nil
	}
	if len(b) == 5 && string(b) == "false" {
		t.Enabled = false
		return nil
	}
	var raw struct {
		Enabled  *bool              `json:"enabled,omitempty"`
		Metadata *shorthandMetadata `json:"metadata,omitempty"`
		Spec     json.RawMessage    `json:"spec,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	t.Enabled = raw.Enabled == nil || *raw.Enabled
	t.Metadata = raw.Metadata
	t.Spec = raw.Spec
	return nil
}

// shorthandTarget accepts either a bool (the common "true" form) or an
// object with `enabled` and selector overrides. Custom unmarshaler handles
// both.
type shorthandTarget struct {
	Enabled           bool               `json:"enabled,omitempty"`
	PodSelector       map[string]string  `json:"-"`
	NamespaceSelector map[string]string  `json:"-"`
	Metadata          *shorthandMetadata `json:"-"`
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
		Metadata          *shorthandMetadata     `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	t.Enabled = raw.Enabled == nil || *raw.Enabled
	t.PodSelector = flattenMatchLabels(raw.PodSelector)
	t.NamespaceSelector = flattenMatchLabels(raw.NamespaceSelector)
	t.Metadata = raw.Metadata
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
