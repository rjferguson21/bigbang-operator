package generator_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
)

// TestGenerate runs each YAML file under testdata/inputs/ through Generate
// and compares the serialized output to its golden under testdata/golden/.
//
// Set UPDATE_GOLDEN=1 to rewrite golden files in place.
func TestGenerate(t *testing.T) {
	scheme := buildScheme(t)

	inputs, err := filepath.Glob("testdata/inputs/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no test inputs found under testdata/inputs/")
	}

	for _, in := range inputs {
		name := strings.TrimSuffix(filepath.Base(in), ".yaml")
		t.Run(name, func(t *testing.T) {
			pkgBytes, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			var pkg bbv1alpha1.Package
			if err := yaml.Unmarshal(pkgBytes, &pkg); err != nil {
				t.Fatalf("unmarshal input: %v", err)
			}

			objs, err := generator.Generate(generator.Input{Package: &pkg, Scheme: scheme})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			got := serializeObjects(t, objs)
			goldenPath := filepath.Join("testdata", "golden", name+".yaml")

			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run UPDATE_GOLDEN=1 to create): %v", err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("output mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
			}
		})
	}
}

func buildScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(bbv1alpha1.AddToScheme(s))
	utilruntime.Must(networkingv1.AddToScheme(s))
	utilruntime.Must(istiosecv1.AddToScheme(s))
	utilruntime.Must(istionetv1.AddToScheme(s))
	return s
}

func serializeObjects(t *testing.T, objs []client.Object) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i, o := range objs {
		if i > 0 {
			buf.WriteString("---\n")
		}
		b, err := yaml.Marshal(o)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(b)
	}
	return buf.Bytes()
}
