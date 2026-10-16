package generator_test

import (
	"testing"

	bb "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
	"sigs.k8s.io/yaml"
)

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
