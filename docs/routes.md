# Routes

Everything under `spec.routes`. Inbound routes expose a service through an
Istio ingress gateway; outbound routes register external hosts for
`REGISTRY_ONLY` egress.

## Inbound

```yaml
routes:
  inbound:
    my-app:
      enabled: true
      gateways:
        - istio-gateway/public-ingressgateway    # <namespace>/<name>
      hosts:
        - my-app.bigbang.mil                     # literal hostnames (no Helm templating)
      service: my-app                            # K8s Service or FQDN
      port: 8080
```

One inbound route emits up to four kinds of resources:

| Resource | Name | When |
|---|---|---|
| `VirtualService` | `my-app` | always |
| `ServiceEntry` | `my-app-internal` | always |
| `NetworkPolicy` | `allow-ingress-to-my-app-8080-from-ns-istio-gateway-pod-public-ingressgateway` (one per gateway) | `networkPolicies.enabled: true` |
| `AuthorizationPolicy` | `my-app-public-ingressgateway-authz-policy` (one per gateway) | `istio.authorizationPolicies.enabled` or ambient — see [authorization-policies.md](authorization-policies.md) |

### Gateway defaults and identities

Set `routes.defaults.inbound.gateways` on a Package to avoid repeating the
list on every route:

```yaml
spec:
  routes:
    defaults:
      inbound:
        gateways:
          - istio-gateway/public-ingressgateway
    inbound:
      dashboard:
        enabled: true
        hosts: [dashboard.example.com]
        service: dashboard
        port: 80
        containerPort: 3000
```

Gateway lists resolve in this order, replacing the whole list:

1. The route's `gateways`.
2. Package `routes.defaults.inbound.gateways`.
3. Global `routes.defaults.inbound.gateways`.

Omitted or null lists inherit; an explicit empty list overrides the lower
level and fails validation on an enabled route. Disabled routes need no
gateway. The resolved list is used by the VirtualService and both policy
types, including routes using TLS passthrough or advanced HTTP rules.

Configure gateway workload labels and identities in the operator's
[global config](global-config.md#ingress-gateways). For example, operator
chart values for an upstream Istio gateway using its default ServiceAccount:

```yaml
globalConfig:
  create: true
  routes:
    defaults:
      inbound:
        gateways: [istio-gateway/public-ingressgateway]
  gateways:
    istio-gateway/public-ingressgateway:
      podSelector:
        matchLabels:
          app: public-ingressgateway
      serviceAccountName: public-ingressgateway
```

The gateway reference remains unchanged in the VirtualService.
`podSelector` controls the NetworkPolicy peer; `serviceAccountName` controls
the AuthorizationPolicy principal. Both identify pods in the namespace from
the gateway reference. The trust domain remains `cluster.local`. Kubernetes
`matchLabels` and `matchExpressions` are supported; an explicit empty selector
matches every pod in that namespace. Each definition requires both fields.

Without a definition, legacy behavior is preserved: the pod selector is
`app.kubernetes.io/name: <gateway-name>` and the identity is
`cluster.local/ns/<gateway-namespace>/sa/<gateway-name>-ingressgateway-service-account`.
Defaults alone do not fix an identity mismatch; define the gateway when its
actual labels or ServiceAccount differ. The operator does not discover or
create gateway workloads.

Gateway names shared by different namespaces in the same Package use
namespace-qualified AuthorizationPolicy suffixes to avoid collisions.
Other existing resource names remain unchanged. This configuration covers
ingress only; outbound waypoint bindings and egress gateways are unsupported.

### The ServiceEntry

Registers the route's *public* hostname so `REGISTRY_ONLY` workloads inside
the mesh can call `https://my-app.bigbang.mil` (the traffic egresses to the
gateway): `MESH_EXTERNAL`, port `443/HTTPS`, resolution `DNS` — or `NONE`,
inferred automatically when any host contains a wildcard (`*.bigbang.mil`),
since Istio rejects DNS resolution for wildcard hosts. An explicit
`resolution:` always wins.

### Optional fields

```yaml
routes:
  inbound:
    my-app:
      containerPort: 9090          # pod port when it differs from the Service port
      selector:                    # pod labels for the netpol/AP;
        app: custom                # defaults to app.kubernetes.io/name=<route-name>
      resolution: STATIC           # DNS | STATIC | DNS_ROUND_ROBIN | NONE
      labels:                      # stamped on every resource this route emits
        team: a
      annotations:
        note: x
```

### TLS passthrough

```yaml
routes:
  inbound:
    secure-app:
      passthrough:
        enabled: true
        gatewayPort: 8443          # default 8443
      service: secure-app
      port: 8443
```

The VirtualService routes on SNI (`spec.tls[]`) instead of HTTP; TLS
terminates at the workload. Mutually exclusive with `http[]`.

### Advanced HTTP rules

```yaml
routes:
  inbound:
    my-app:
      http:                        # raw Istio HTTPRoute entries, used verbatim
        - match:
            - uri:
                prefix: /api
          rewrite:
            uri: /
          retries:
            attempts: 3
            perTryTimeout: 2s
          route:
            - destination:
                host: my-app
                port:
                  number: 8080
```

When `http[]` is present it replaces the generated single-destination route.
Entries are JSON round-tripped into the Istio proto, so typos fail the
reconcile instead of applying silently-wrong config.

### Validation

`routes.inbound` is schema-opaque to the apiserver
(x-kubernetes-preserve-unknown-fields), so the reconciler validates instead:
missing effective `gateways`/`service`/`port`, or a gateway not shaped
`<namespace>/<name>` with valid Kubernetes names, surface as `Ready=False / GenerationFailed` on the
Package.

## Outbound

```yaml
routes:
  outbound:
    external-api:
      enabled: true
      hosts:
        - api.example.com
      location: MESH_EXTERNAL      # default; or MESH_INTERNAL
      resolution: DNS              # default
      ports:
        - number: 443
          name: https
          protocol: TLS
      metadata:
        labels:
          team: a
```

Emits one `ServiceEntry` named `external-api-external` (`-internal` when
`location: MESH_INTERNAL`). Omitted `ports` default to a single
`443/https/HTTPS` entry.

## prependReleaseName

`routes.prependReleaseName: true` prefixes route resource names with the
Package name; independent of the istio/networkPolicies flags.

## Ambient integration test

`test/integration/ambient_gateway_test.go` exercises generated routing and
policies against a live ambient mesh with an existing ingress gateway. It
creates a temporary namespace and workload, verifies HTTP 200 with the
configured identity, HTTP 503 with an incorrect identity, then HTTP 200
after restoration. It cleans up its resources. The generator is invoked
directly so the test does not replace a running operator; the controller's
ConfigMap watch is covered separately by envtest.

For a local Big Bang development cluster, set the actual gateway address,
pod labels, and ServiceAccount:

```sh
AMBIENT_GATEWAY=istio-gateway/public-ingressgateway \
AMBIENT_GATEWAY_ADDRESS=https://172.28.0.3 \
AMBIENT_GATEWAY_SERVICE_ACCOUNT=public-ingressgateway \
AMBIENT_GATEWAY_SELECTOR='{"matchLabels":{"app":"public-ingressgateway"}}' \
AMBIENT_GATEWAY_HOST_SUFFIX=dev.bigbang.mil \
AMBIENT_KYVERNO_EXCEPTION_NAMESPACE=kyverno \
go test -tags=integration ./test/integration -run TestAmbientGatewayIdentity -count=1 -v
```

The gateway must serve the hostname suffix and reach the test workload.
The test skips when required gateway variables are absent. The optional
Kyverno variable creates a registry-policy exception scoped to the test
namespace; omit it on clusters without that policy. The workload image
defaults to `ghcr.io/stefanprodan/podinfo:6.13.0` and can be replaced with a
compatible image through `AMBIENT_WORKLOAD_IMAGE`. TLS certificate verification
is disabled for this development-cluster test.
