package generator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

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
	r := &parsedK8sRemote{Protocol: "TCP"}
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
	r := &parsedK8sRemote{Protocol: "TCP"}
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
	r := &parsedLocalIngressKey{Protocol: "TCP"}
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
	r := &parsedCIDR{Protocol: "TCP"}
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
	return &parsedCIDR{CIDR: key, Protocol: "TCP"}, nil
}

// cidrNameSegment mirrors bb-common: replace `.` and `/` with `-`.
// `0.0.0.0/0` collapses to "anywhere" at the caller.
func cidrNameSegment(cidr string) string {
	out := strings.ReplaceAll(cidr, ".", "-")
	return strings.ReplaceAll(out, "/", "-")
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

// namePortSuffix mirrors bb-common's `name-ports.tpl`:
//
//	no ports        -> "any-port"
//	one, no range   -> "port-<n>"
//	range           -> "ports-<begin>-thru-<end>"
//	list (>1)       -> "ports-<a>-<b>-<c>..."
func namePortSuffix(ports []int, hasRange bool) string {
	if len(ports) == 0 {
		return "any-port"
	}
	if hasRange {
		return fmt.Sprintf("ports-%d-thru-%d", ports[0], ports[1])
	}
	prefix := "port"
	if len(ports) > 1 {
		prefix = "ports"
	}
	var b strings.Builder
	b.WriteString(prefix)
	for _, p := range ports {
		b.WriteString("-")
		b.WriteString(strconv.Itoa(p))
	}
	return b.String()
}
