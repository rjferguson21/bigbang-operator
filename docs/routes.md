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
missing `gateways`/`service`/`port`, or a gateway not shaped
`<namespace>/<name>`, surface as `Ready=False / GenerationFailed` on the
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
