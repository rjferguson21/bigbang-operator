# Istio resources

Everything under `spec.istio`. Nothing is emitted unless
`istio.enabled: true`.

## PeerAuthentication

Always emitted when istio is enabled:

```yaml
istio:
  enabled: true
  mtls:
    mode: STRICT      # STRICT (default) | PERMISSIVE | DISABLE | UNSET
```

→ `PeerAuthentication/default-peer-auth` with `spec.mtls.mode` set
accordingly. (`UNSET` is an operator extension; bb-common's schema stops at
DISABLE.)

## Sidecar

```yaml
istio:
  enabled: true
  sidecar:
    enabled: true
    outboundTrafficPolicyMode: REGISTRY_ONLY   # default; or ALLOW_ANY
```

→ `Sidecar/sidecar`, namespace-wide (no workloadSelector), setting
`spec.outboundTrafficPolicy.mode`. `REGISTRY_ONLY` is the locked-down mode —
sidecars may only reach services in the mesh registry, which is why routes
also emit ServiceEntries (see [routes.md](routes.md)).

**Suppressed under ambient**: when `istio.ambient.enabled: true` there are no
sidecars to configure, so the resource is not emitted even if
`sidecar.enabled: true`.

## Ambient mode

`istio.ambient.enabled: true` is one switch with several effects, matching
bb-common's effective-values logic:

| Effect | Where |
|---|---|
| `Sidecar` resource suppressed | here |
| AuthorizationPolicy emission force-enabled | [authorization-policies.md](authorization-policies.md) |
| AP generation from netpol shorthand force-enabled | same |
| HBONE port 15008 injection force-enabled | [network-policies.md](network-policies.md) |
| `default-egress-allow-istiod` suppressed | same |
| `default-ingress-allow-prometheus-to-istio-sidecar` suppressed | same |
| `default-ingress-allow-ambient-kubelet` emitted | same |

## Custom ServiceEntries

```yaml
istio:
  enabled: true
  serviceEntries:
    custom:
      - name: external-db
        labels:
          tier: data
        spec:
          hosts:
            - db.example.com
          location: MESH_EXTERNAL
          resolution: DNS
          ports:
            - number: 5432
              name: tcp-postgres
              protocol: TCP
```

The spec is passed through to a `ServiceEntry` verbatim (JSON round-tripped
into the Istio proto, so invalid fields fail the reconcile rather than
applying garbage).

## Custom AuthorizationPolicies

Two forms, both under `istio.authorizationPolicies` — see
[authorization-policies.md](authorization-policies.md#4-custom-policies).

## prependReleaseName

`istio.prependReleaseName: true` prefixes this section's resource names with
the Package name (`myapp-default-peer-auth`, `myapp-sidecar`). The
`networkPolicies` and `routes` sections each have their own independent flag,
matching bb-common.
