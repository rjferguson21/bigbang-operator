package helmhooks_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	bb "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
	"bigbang.dev/operator/pkg/helmhooks"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

const admissionValues = `networkPolicies:
  enabled: true
  defaultsAsHooks:
    enabled: true
  egress:
    from:
      admission:
        podSelector:
          matchLabels:
            app.kubernetes.io/instance: '{{ .Release.Name }}'
            app.kubernetes.io/component: prometheus-operator-webhook
        metadata:
          annotations:
            helm.sh/hook: pre-install,pre-upgrade,post-install,post-upgrade,pre-rollback,post-rollback,pre-delete,post-delete
            helm.sh/hook-weight: '-10'
            helm.sh/hook-delete-policy: hook-succeeded
        to:
          definition:
            kubeAPI: true
          k8s:
            dependencies/backend:8080: true
`

func render(t *testing.T, values string) ([]unstructured.Unstructured, string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required for chart rendering tests")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(dir, "charts", "bigbang-operator-package"), os.DirFS(filepath.Join("..", "..", "helm", "package-integration"))); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		"Chart.yaml":             "apiVersion: v2\nname: test-package\nversion: 0.1.0\n",
		"values.yaml":            values,
		"templates/package.yaml": `{{ include "bigbang-operator.package" . }}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("helm", "template", "monitoring", dir, "--namespace", "shared")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, string(output), err
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(output), 4096)
	var objects []unstructured.Unstructured
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if object.GetKind() != "" {
			objects = append(objects, object)
		}
	}
	return objects, string(output), nil
}

func TestHelmHookRendering(t *testing.T) {
	for _, ambient := range []bool{false, true} {
		t.Run(strconv.FormatBool(ambient), func(t *testing.T) {
			values := admissionValues
			if ambient {
				values += "\nistio:\n  enabled: true\n  ambient:\n    enabled: true\n"
			}
			objects, out, err := render(t, values)
			if err != nil {
				t.Fatalf("render: %v\n%s", err, out)
			}
			var pkg bb.Package
			var hooks []unstructured.Unstructured
			cleanups := 0
			names := map[string]bool{}
			for _, obj := range objects {
				if names[obj.GetName()] {
					t.Fatalf("duplicate resource name %s", obj.GetName())
				}
				names[obj.GetName()] = true
				if obj.GetKind() == "Package" {
					if obj.GetAnnotations()[helmhooks.Action] == "cleanup" {
						cleanups++
						if obj.GetAnnotations()["helm.sh/hook-weight"] != "10000" {
							t.Fatal("cleanup must follow jobs")
						}
					} else {
						data, err := obj.MarshalJSON()
						if err != nil {
							t.Fatal(err)
						}
						if err := yaml.Unmarshal(data, &pkg); err != nil {
							t.Fatal(err)
						}
					}
				} else if obj.GetKind() == "NetworkPolicy" {
					hooks = append(hooks, obj)
				} else {
					t.Fatalf("unexpected helper resource %s", obj.GetKind())
				}
			}
			if cleanups != 4 || len(hooks) < 3 {
				t.Fatalf("cleanup=%d policies=%d", cleanups, len(hooks))
			}
			if pkg.Name != "monitoring" || len(generator.Warnings(&pkg)) != 0 {
				t.Fatal("missing integrated persistent Package")
			}
			scheme := runtime.NewScheme()
			if err := networkingv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			// Render network policies separately; Istio resource GVKs are covered by the generator suite.
			spec := pkg.Spec.Istio
			pkg.Spec.Istio = nil
			persistent, err := generator.Generate(generator.Input{Package: &pkg, Scheme: scheme})
			pkg.Spec.Istio = spec
			if err != nil {
				t.Fatal(err)
			}
			for _, obj := range persistent {
				if names[obj.GetName()] {
					t.Fatalf("hook collides with persistent %s", obj.GetName())
				}
				if obj.GetAnnotations()["helm.sh/hook"] != "" {
					t.Fatal("persistent policy retains hook annotations")
				}
			}
			checkHookPolicies(t, hooks, pkg, ambient)
		})
	}
}

func TestHelmOrdinaryPackageAndNestedValues(t *testing.T) {
	objects, out, err := render(t, "networkPolicies:\n  enabled: true\n")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(objects) != 1 || objects[0].GetKind() != "Package" {
		t.Fatal("ordinary package acquired hooks")
	}
	var nested strings.Builder
	nested.WriteString("bb-common:\n")
	for _, line := range strings.Split(admissionValues, "\n") {
		nested.WriteString("  " + line + "\n")
	}
	objects, out, err = render(t, nested.String())
	if err != nil || len(objects) < 4 {
		t.Fatalf("nested values: %v: %s", err, out)
	}
}

func TestHelmRejectsInvalidLifecycle(t *testing.T) {
	for _, values := range []string{
		admissionValues + "\noperatorHooks:\n  timeoutSeconds: 0\n",
		admissionValues + "\noperatorHooks:\n  cleanupWeight: -20\n",
		strings.ReplaceAll(admissionValues, "'-10'", "'oops'"),
		strings.ReplaceAll(admissionValues, "pre-install,pre-upgrade,post-install,post-upgrade,pre-rollback,post-rollback,pre-delete,post-delete", "test"),
	} {
		if _, out, err := render(t, values); err == nil {
			t.Fatalf("expected rejection:\n%s", out)
		}
	}
}

func checkHookPolicies(t *testing.T, hooks []unstructured.Unstructured, pkg bb.Package, ambient bool) {
	t.Helper()
	apiRules, backendRules := 0, 0
	for _, obj := range hooks {
		a := obj.GetAnnotations()
		if a["helm.sh/hook-delete-policy"] != "before-hook-creation" {
			t.Fatal("unsafe policy deletion")
		}
		if obj.GetLabels()["bigbang.dev/package"] != "" || len(obj.GetOwnerReferences()) != 0 {
			t.Fatal("hook enters persistent ownership")
		}
		if a[helmhooks.Token] != pkg.Annotations[helmhooks.Token] {
			t.Fatal("cleanup token mismatch")
		}
		var policy networkingv1.NetworkPolicy
		data, _ := obj.MarshalJSON()
		if err := yaml.Unmarshal(data, &policy); err != nil {
			t.Fatal(err)
		}
		if a["generated.network-policies.bigbang.dev/local-key"] != "admission" {
			continue
		}
		if policy.Spec.PodSelector.MatchLabels["app.kubernetes.io/instance"] != "monitoring" {
			t.Fatal("release selector not expanded")
		}
		if policy.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"] != "prometheus-operator-webhook" {
			t.Fatal("hook selector lost")
		}
		if a["helm.sh/hook-weight"] != "-10" {
			t.Fatal("hook ordering lost")
		}
		if a["generated.network-policies.bigbang.dev/from-definition"] == "kubeAPI" {
			apiRules++
		}
		for _, rule := range policy.Spec.Egress {
			for _, port := range rule.Ports {
				if port.Port != nil && port.Port.IntValue() == 8080 {
					backendRules++
					hbone := false
					for _, p := range rule.Ports {
						if p.Port != nil && p.Port.IntValue() == 15008 {
							hbone = true
						}
					}
					if hbone != ambient {
						t.Fatalf("ambient HBONE=%t want %t", hbone, ambient)
					}
				}
			}
		}
	}
	if apiRules != 1 || backendRules != 1 {
		t.Fatalf("API=%d backend=%d", apiRules, backendRules)
	}
}

func TestHelmPerRuleHooksWithoutDefaults(t *testing.T) {
	values := strings.Replace(admissionValues, "defaultsAsHooks:\n    enabled: true", "defaultsAsHooks:\n    enabled: false", 1)
	values = strings.ReplaceAll(values, "'-10'", "0")
	values = strings.ReplaceAll(values, "pre-install,pre-upgrade", "pre-install, pre-upgrade")
	objects, out, err := render(t, values)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	count := 0
	for _, obj := range objects {
		if obj.GetKind() != "NetworkPolicy" {
			continue
		}
		count++
		if obj.GetAnnotations()["helm.sh/hook-weight"] != "0" {
			t.Fatal("explicit zero hook weight changed")
		}
		if strings.Contains(obj.GetAnnotations()["helm.sh/hook"], " ") {
			t.Fatal("hook phases not normalized")
		}
	}
	if count != 2 {
		t.Fatalf("expected only the two declared allows, got %d", count)
	}
}
