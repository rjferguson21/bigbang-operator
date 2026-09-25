package generator

import (
	"fmt"
	"strconv"
	"strings"
)

// Resource-name construction. All generated names mirror bb-common's
// templates so a migrated package keeps identical resource names.

const (
	// cidrAnywhere is the open-internet CIDR; rendered as "anywhere" in
	// generated NetworkPolicy/AP names.
	cidrAnywhere = "0.0.0.0/0"

	// nameAnyPod is the placeholder used in NetworkPolicy names when the
	// local shorthand key is "*" (rule applies to every pod in the namespace).
	nameAnyPod = "any-pod"
)

// prependName mirrors bb-common's `prependReleaseName` behavior: when true,
// emitted names get the package name prefixed (e.g. "myapp-default-peer-auth").
func prependName(prepend bool, releaseName, name string) string {
	if !prepend {
		return name
	}
	return releaseName + "-" + name
}

// cidrNameSegment mirrors bb-common: replace `.` and `/` with `-`.
// `0.0.0.0/0` collapses to "anywhere" at the caller.
func cidrNameSegment(cidr string) string {
	out := strings.ReplaceAll(cidr, ".", "-")
	return strings.ReplaceAll(out, "/", "-")
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
