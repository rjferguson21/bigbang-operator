# AuthorizationPolicies

Istio L7 enforcement layered on top of the L3/L4 NetworkPolicies. Four
sources, all gated on:

```yaml
istio:
  enabled: true
  authorizationPolicies:
    enabled: true          # or istio.ambient.enabled: true, which forces this on
```

## 1. Defaults

| Name | Effect | Disable with |
|---|---|---|
| `default-authz-allow-nothing` | empty spec = deny everything (the L7 baseline) | `authorizationPolicies.defaults.denyAll.enabled: false` |
| `default-authz-allow-all-in-ns` | ALLOW from the package's own namespace | `authorizationPolicies.defaults.allowInNamespace.enabled: false` |

`authorizationPolicies.defaults.enabled: false` gates both. Both are also
suppressed when `networkPolicies.ingress.defaults.enabled: false` — the L7
defaults track the L4 ingress defaults, matching bb-common.

## 2. Generated from NetworkPolicy shorthand

When `authorizationPolicies.generateFromNetpol: true` (the default), every
ingress shorthand rule emits a companion AP named after its NetworkPolicy so
the pair correlates in dashboards.

```yaml
networkPolicies:
  ingress:
    to:
      api:8080:
        from:
          k8s:
            backend/worker: true          # → AP allowing source *namespace*
            api-sa@backend/worker: true   # → AP pinning the SPIFFE principal
          cidr:
            192.168.1.0/24: true          # → AP with source ipBlocks
```

- Plain `ns/pod` rules allow by **namespace** (`source.namespaces`).
- `identity@ns/pod` rules allow by **principal**
  (`cluster.local/ns/<ns>/sa/<identity>`) — name gains
  `-with-identity-<sa>`.
- Ports from the local key become `to.operation.ports` (ranges are expanded).
- Rule-level and pod-level `metadata` labels/annotations land on the AP too.

## 3. Per-route gateway pinning

Each inbound route emits one AP per gateway restricting traffic to that
gateway's ServiceAccount:

```yaml
# routes.inbound.my-app with gateway istio-gateway/public-ingressgateway →
# AuthorizationPolicy/my-app-public-ingressgateway-authz-policy
spec:
  action: ALLOW
  selector:
    matchLabels: <route selector>
  rules:
    - from:
        - source:
            namespaces:
              - istio-gateway
            principals:
              - cluster.local/ns/istio-gateway/sa/public-ingressgateway-ingressgateway-service-account
      to:
        - operation:
            ports:
              - "<route port>"
```

The ServiceAccount follows Big Bang's gateway naming convention
(`<gateway-name>-ingressgateway-service-account`).

## 4. Custom policies

Two passthrough forms (mirroring bb-common):

```yaml
istio:
  authorizationPolicies:
    custom:                      # list form — always emitted
      - name: allow-special
        spec:
          action: ALLOW
          rules:
            - from:
                - source:
                    namespaces:
                      - special-ns
    additionalPolicies:          # map form — per-entry enabled flag
      audit-everything:
        enabled: true            # default true
        name: optional-override  # defaults to the map key
        spec:
          action: AUDIT
          rules:
            - {}               # audit all requests
```

Specs are JSON round-tripped into the Istio proto; malformed specs fail the
reconcile with `Ready=False / GenerationFailed`.

## A note on `action: ALLOW` in output

Istio's proto encodes `ALLOW` as the zero value, so the operator's emitted
YAML omits the `action` field where bb-common writes `action: ALLOW`
explicitly. Istio treats the two identically. The same applies to
ServiceEntry `location: MESH_EXTERNAL` and `resolution: NONE`.
