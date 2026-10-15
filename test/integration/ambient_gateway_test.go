//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	istionetv1 "istio.io/client-go/pkg/apis/networking/v1"
	istiosecv1 "istio.io/client-go/pkg/apis/security/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	bbv1alpha1 "bigbang.dev/operator/api/v1alpha1"
	"bigbang.dev/operator/pkg/generator"
)

// TestAmbientGatewayIdentity exercises generated resources on a live mesh.
// Controller watch/reconcile behavior is covered separately by envtest.
func TestAmbientGatewayIdentity(t *testing.T) {
	ref := os.Getenv("AMBIENT_GATEWAY")
	address := os.Getenv("AMBIENT_GATEWAY_ADDRESS")
	sa := os.Getenv("AMBIENT_GATEWAY_SERVICE_ACCOUNT")
	selector := os.Getenv("AMBIENT_GATEWAY_SELECTOR")
	if ref == "" || address == "" || sa == "" || selector == "" {
		t.Skip("set AMBIENT_GATEWAY, AMBIENT_GATEWAY_ADDRESS, AMBIENT_GATEWAY_SERVICE_ACCOUNT, and AMBIENT_GATEWAY_SELECTOR")
	}
	parts := strings.Split(ref, "/")
	if len(parts) != 2 || sa == parts[1]+"-ingressgateway-service-account" {
		t.Fatal("use a namespace/name gateway with a non-legacy ServiceAccount")
	}
	ns := fmt.Sprintf("ingress-identity-%d", time.Now().UnixNano())
	suffix := os.Getenv("AMBIENT_GATEWAY_HOST_SUFFIX")
	if suffix == "" {
		suffix = "dev.bigbang.mil"
	}
	host := ns + "." + suffix
	image := os.Getenv("AMBIENT_WORKLOAD_IMAGE")
	if image == "" {
		image = "ghcr.io/stefanprodan/podinfo:6.13.0"
	}
	kubectl(t, "", "create", "namespace", ns)
	t.Cleanup(func() { kubectl(t, "", "delete", "namespace", ns, "--wait=false") })
	kubectl(t, "", "label", "namespace", ns, "istio.io/dataplane-mode=ambient")
	if exceptionNS := os.Getenv("AMBIENT_KYVERNO_EXCEPTION_NAMESPACE"); exceptionNS != "" {
		kubectl(t, fmt.Sprintf(`apiVersion: kyverno.io/v2
kind: PolicyException
metadata: {name: %s, namespace: %s}
spec:
  exceptions:
    - policyName: restrict-image-registries
      ruleNames: [validate-registries, autogen-validate-registries]
  match:
    any:
      - resources:
          kinds: [Pod, Deployment, ReplicaSet]
          namespaces: [%s]
`, ns, exceptionNS, ns), "apply", "-f", "-")
		t.Cleanup(func() { kubectl(t, "", "-n", exceptionNS, "delete", "policyexception", ns, "--ignore-not-found") })
	}
	kubectl(t, fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: %s}
spec:
  replicas: 1
  selector: {matchLabels: {app.kubernetes.io/name: web}}
  template:
    metadata: {labels: {app.kubernetes.io/name: web}}
    spec:
      securityContext: {runAsNonRoot: true, runAsUser: 10001, seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: web
          image: %s
          ports: [{containerPort: 9898}]
          resources:
            requests: {cpu: 10m, memory: 32Mi}
            limits: {cpu: 100m, memory: 128Mi}
          securityContext: {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}
---
apiVersion: v1
kind: Service
metadata: {name: web, namespace: %s}
spec:
  selector: {app.kubernetes.io/name: web}
  ports: [{name: http, port: 9898, targetPort: 9898}]
`, ns, image, ns), "apply", "-f", "-")
	kubectl(t, "", "-n", ns, "rollout", "status", "deployment/web", "--timeout=180s")
	var pods corev1.PodList
	if err := json.Unmarshal([]byte(kubectl(t, "", "-n", ns, "get", "pods", "-o", "json")), &pods); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Annotations["ambient.istio.io/redirection"] != "enabled" ||
		len(pods.Items[0].Spec.Containers) != 1 {
		t.Fatal("workload must be enrolled in ambient without a sidecar")
	}
	var pkg bbv1alpha1.Package
	if err := yaml.UnmarshalStrict([]byte(fmt.Sprintf(`metadata: {name: web, namespace: %s}
spec:
  istio: {enabled: true, ambient: {enabled: true}}
  networkPolicies: {enabled: true}
  routes:
    inbound:
      web: {enabled: true, service: web, port: 9898, hosts: [%s]}
`, ns, host)), &pkg); err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		bbv1alpha1.AddToScheme, networkingv1.AddToScheme, istionetv1.AddToScheme, istiosecv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(identity string) {
		t.Helper()
		shared, err := generator.ParseIngressConfig(
			fmt.Sprintf("defaults: {inbound: {gateways: [%s]}}", ref),
			fmt.Sprintf("%s: {podSelector: %s, serviceAccountName: %s}", ref, selector, identity),
		)
		if err != nil {
			t.Fatal(err)
		}
		objects, err := generator.Generate(generator.Input{Package: &pkg, Scheme: scheme, SharedIngressConfig: shared})
		if err != nil {
			t.Fatal(err)
		}
		var manifests bytes.Buffer
		for _, obj := range objects {
			// No Package is installed: avoid an existing operator reconciling
			// this fixture while exercising the current generator's output.
			obj.SetOwnerReferences(nil)
			data, err := yaml.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			manifests.WriteString("---\n")
			manifests.Write(data)
		}
		kubectl(t, manifests.String(), "apply", "--server-side", "--field-manager=ingress-integration", "-f", "-")
	}
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, ServerName: host}, // Development gateway certificate.
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	expectStatus := func(want int) {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		var last string
		consecutive := 0
		for time.Now().Before(deadline) {
			req, err := http.NewRequest(http.MethodGet, strings.TrimRight(address, "/")+"/healthz", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = host
			resp, err := client.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				last = resp.Status
				if resp.StatusCode == want {
					consecutive++
				} else {
					consecutive = 0
				}
				if consecutive == 3 {
					return
				}
			} else {
				last = err.Error()
				consecutive = 0
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("gateway response = %s, want HTTP %d", last, want)
	}
	apply(sa)
	expectStatus(http.StatusOK)
	apply("wrong-gateway-identity")
	expectStatus(http.StatusServiceUnavailable)
	apply(sa)
	expectStatus(http.StatusOK)
	t.Log("ambient ingress allowed the configured identity, rejected a wrong identity, and recovered")
}

func kubectl(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %v: %v\n%s", args, err, out)
	}
	return string(out)
}
