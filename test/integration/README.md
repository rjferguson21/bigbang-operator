# Helm 4 chart and lifecycle validation

Run `make test-helm` with Docker, k3d 5.8.3, kubectl, curl, and Helm
4.3.1 available. It creates a temporary registry and k3d cluster, installs Istio
CRDs, builds the operator, and runs chart and operator lifecycle tests.
The current kubeconfig is not changed. Its cluster, registry, and test image are
removed on exit. See [development](../../docs/development.md) for coverage.

# Ambient ingress identity test

Use a cluster with Istio ambient and NetworkPolicy enforcement. Supply an Istio
Gateway whose ServiceAccount differs from the operator's legacy fallback
`<gateway-name>-ingressgateway-service-account`, plus an HTTPS address and a host
suffix accepted by that Gateway:

```sh
AMBIENT_GATEWAY=istio-gateway/public \
AMBIENT_GATEWAY_ADDRESS=https://172.28.0.3 \
AMBIENT_GATEWAY_SERVICE_ACCOUNT=public-ingressgateway \
AMBIENT_GATEWAY_SELECTOR='{matchLabels: {app: public-ingressgateway}}' \
AMBIENT_GATEWAY_HOST_SUFFIX=dev.bigbang.mil \
go test -tags=integration ./test/integration -run TestAmbientGatewayIdentity -v -count=1
```

The test creates a temporary ambient namespace and applies generated resources
directly. It verifies HTTP 200 with the configured identity, HTTP 503 with a wrong
identity, and recovery after restoring the configuration. Controller reconciliation
and global ConfigMap updates are covered by envtest separately.

The default workload image is `ghcr.io/stefanprodan/podinfo:6.13.0`; override it with
`AMBIENT_WORKLOAD_IMAGE` if needed. If Kyverno blocks that registry, setting
`AMBIENT_KYVERNO_EXCEPTION_NAMESPACE` creates a registry-policy exception scoped to
the test namespace. The namespace and exception are removed afterward.
