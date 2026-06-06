# Default resources generated per Package

For each `Package` CR the operator renders bb-common with the package's `spec`
and applies the resulting manifests. This document lists the resources
bb-common emits **by default** — i.e. with only top-level toggles flipped on
and no further overrides. Names below assume the package namespace `<ns>`;
prepend the release name when `prependReleaseName: true`.

Sources: bb-common test files under `bb-common/chart/tests/`.

## Istio (`istio.enabled: true`)

From `templates/istio/`. Emitted by default:

| Kind | Name | Notes |
|---|---|---|
| `PeerAuthentication` (`security.istio.io/v1`) | `default-peer-auth` | mTLS mode from `istio.mtls.mode` (default `STRICT`). See `tests/istio/peer-auth_test.yaml`. |

Conditionally emitted:

| Trigger | Kind | Name |
|---|---|---|
| `istio.sidecar.enabled: true` | `Sidecar` (`networking.istio.io/v1`) | `sidecar` — `outboundTrafficPolicy.mode` from `istio.sidecar.outboundTrafficPolicyMode` (default `REGISTRY_ONLY`). |
| `istio.authorizationPolicies.enabled: true` **or** `istio.ambient.enabled: true`, plus `networkPolicies.ingress.defaults.enabled: true` | `AuthorizationPolicy` × 2 | `default-authz-allow-nothing` (empty `spec: {}` — deny-all) and `default-authz-allow-all-in-ns` (ALLOW from same namespace). See `tests/istio/authorization-policies-defaults_test.yaml`. |
| `istio.authorizationPolicies.enabled: true` and `generateFromNetpol: true` | `AuthorizationPolicy` × N | One per ingress NetworkPolicy, mirroring its source selectors. |

Custom resources (`istio.serviceEntries.custom`, `istio.authorizationPolicies.custom`, `istio.authorizationPolicies.additionalPolicies`) are passed through verbatim.

## Network Policies (`networkPolicies.enabled: true`)

From `templates/network-policies/`. With only `networkPolicies.enabled: true`
set (defaults left alone), the following are emitted because each default's
`enabled` defaults to `true`:

| Direction | Kind | Name | Effect |
|---|---|---|---|
| egress | `NetworkPolicy` | `default-egress-deny-all` | Deny all egress (baseline). |
| egress | `NetworkPolicy` | `default-egress-allow-all-in-ns` | Allow egress within the same namespace. |
| egress | `NetworkPolicy` | `default-egress-allow-kube-dns` | UDP/TCP 53 to `kube-system/kube-dns`. |
| egress | `NetworkPolicy` | `default-egress-allow-istiod` | TCP 15012 to `istio-system/istiod`. Skipped when `istio.ambient.enabled: true`. |
| ingress | `NetworkPolicy` | `default-ingress-deny-all` | Deny all ingress (baseline). |
| ingress | `NetworkPolicy` | `default-ingress-allow-all-in-ns` | Allow ingress from the same namespace. |
| ingress | `NetworkPolicy` | `default-ingress-allow-prometheus-to-istio-sidecar` | TCP 15020 from `monitoring/prometheus`. |

All default policies carry the labels:

```yaml
network-policies.bigbang.dev/source: bb-common
network-policies.bigbang.dev/direction: egress  # or ingress
```

Disable a single default with e.g. `networkPolicies.egress.defaults.allowIstiod.enabled: false`,
or disable all with `networkPolicies.{egress,ingress}.defaults.enabled: false`.

Hook variants (`...-as-hook`) are also created when
`networkPolicies.defaultsAsHooks.enabled: true`.

Beyond defaults, three shapes generate `NetworkPolicy` resources:

1. **Shorthand egress** — `networkPolicies.egress.from.<src>.to.k8s.<ns>/<pod>[:port]: true`
   → `allow-egress-from-<src>-to-ns-<ns>-pod-<pod>-<protocol>-port-<port>`
2. **Shorthand ingress** — `networkPolicies.ingress.to.<dst>.from.k8s.<ns>/<pod>[:port]: true`
   → `allow-ingress-to-<dst>-<protocol>-port-<port>-from-ns-<ns>-pod-<pod>`
3. **Raw specs** — `networkPolicies.additionalPolicies[]` (or legacy `additional[]`) — name and spec used as-is.

Duplicate-spec policies are de-duplicated; same name with different specs gets `-deduped-N` suffixes (see `tests/network-policies/ingress_test.yaml`).

## Routes (`routes.inbound.<name>` / `routes.outbound.<name>`)

From `templates/routes/`. Nothing is emitted by default — routes only appear
when at least one entry is declared under `routes.inbound` or `routes.outbound`.

Per inbound route `<name>`:

| Kind | apiVersion | Notes |
|---|---|---|
| `VirtualService` | `networking.istio.io/v1` | Bound to `gateways[]`, routing `hosts[]` → `service:port`. |
| `ServiceEntry` | `networking.istio.io/v1` | `<name>-internal`, location `MESH_EXTERNAL`, resolution from `routes.inbound.<name>.resolution` (default `DNS`). |
| `NetworkPolicy` | `networking.k8s.io/v1` | `allow-ingress-to-<service>-<port>-from-ns-<gw-ns>-pod-<gw-name>` — only when `networkPolicies.enabled: true`. |
| `AuthorizationPolicy` | `security.istio.io/v1` | `<service>-<gw-name>-authz-policy` — only when `istio.authorizationPolicies.enabled: true`. Restricts to the gateway's ServiceAccount. |

See `tests/routes/simple_test.yaml` for the canonical shape.

Outbound routes generate `ServiceEntry` (and optionally companion policies)
for egress to external hosts.

## Summary: minimum default footprint

For a `Package` with `istio.enabled: true`, `networkPolicies.enabled: true`,
and one entry in `routes.inbound`, the operator applies roughly:

- 1 × `PeerAuthentication`
- 7 × `NetworkPolicy` (4 egress defaults + 3 ingress defaults — minus `allow-istiod` under ambient)
- 1 × `VirtualService`, 1 × `ServiceEntry`, 1 × `NetworkPolicy` per inbound route
- (+ 2 × `AuthorizationPolicy` defaults and 1 × per route if `istio.authorizationPolicies.enabled`)
