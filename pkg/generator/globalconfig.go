package generator

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
)

// Data keys in the operator's global ConfigMap.
const (
	GlobalConfigEgressKey  = "egressDefinitions"
	GlobalConfigIngressKey = "ingressDefinitions"
)

// ParseSharedEgressDefinitions parses the global ConfigMap's
// egressDefinitions value: a YAML (or JSON) map of definition name to the
// same schema as `networkPolicies.egress.definitions.<name>`. Strict mode —
// unknown fields are errors, matching the CRD's pruning of package-local
// definitions.
func ParseSharedEgressDefinitions(data string) (map[string]bbv1alpha1.NetworkPoliciesEgressDefinitionsValue, error) {
	if strings.TrimSpace(data) == "" {
		return nil, nil
	}
	out := map[string]bbv1alpha1.NetworkPoliciesEgressDefinitionsValue{}
	if err := yaml.UnmarshalStrict([]byte(data), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", GlobalConfigEgressKey, err)
	}
	return out, nil
}

// ParseSharedIngressDefinitions is the ingress counterpart, keyed by
// ingressDefinitions and matching the schema of
// `networkPolicies.ingress.definitions.<name>`.
func ParseSharedIngressDefinitions(data string) (map[string]bbv1alpha1.NetworkPoliciesIngressDefinitionsValue, error) {
	if strings.TrimSpace(data) == "" {
		return nil, nil
	}
	out := map[string]bbv1alpha1.NetworkPoliciesIngressDefinitionsValue{}
	if err := yaml.UnmarshalStrict([]byte(data), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", GlobalConfigIngressKey, err)
	}
	return out, nil
}
