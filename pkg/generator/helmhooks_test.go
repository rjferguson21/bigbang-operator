package generator_test

import (
	"testing"

	bb "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
	"sigs.k8s.io/yaml"
)

func TestIntegratedHookAnnotationsDoNotMutatePackage(t *testing.T) {
	var pkg bb.Package
	if err := yaml.Unmarshal([]byte(`apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: example
  annotations:
    hooks.bigbang.dev/integration: 'true'
spec:
  istio:
    enabled: true
    authorizationPolicies:
      custom:
        - name: custom
          annotations:
            helm.sh/hook: pre-install
            example.dev/note: retained
          spec: {}
`), &pkg); err != nil {
		t.Fatal(err)
	}
	objects, err := generator.Generate(generator.Input{Package: &pkg, Scheme: buildScheme(t)})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Spec.Istio.AuthorizationPolicies.Custom[0].Annotations["helm.sh/hook"] != "pre-install" {
		t.Fatal("generator mutated input hook declaration")
	}
	found := false
	for _, obj := range objects {
		if obj.GetName() != "custom" {
			continue
		}
		found = true
		if obj.GetAnnotations()["helm.sh/hook"] != "" {
			t.Fatal("persistent resource contains hook metadata")
		}
		if obj.GetAnnotations()["example.dev/note"] != "retained" {
			t.Fatal("unrelated annotation lost")
		}
	}
	if !found {
		t.Fatal("custom policy missing")
	}
}

func TestAmbientDefaultsWithoutAuthorizationSettings(t *testing.T) {
	var pkg bb.Package
	if err := yaml.Unmarshal([]byte(`metadata:
  name: ambient
spec:
  istio:
    enabled: true
    ambient:
      enabled: true
`), &pkg); err != nil {
		t.Fatal(err)
	}
	objects, err := generator.Generate(generator.Input{Package: &pkg, Scheme: buildScheme(t)})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, obj := range objects {
		if obj.GetObjectKind().GroupVersionKind().Kind == "AuthorizationPolicy" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected default ambient authorization policies, got %d", count)
	}
}
