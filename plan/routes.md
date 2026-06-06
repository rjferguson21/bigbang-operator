# `spec.routes`

bb-common keys under `routes` generate the Istio routing resources
(`VirtualService`, `ServiceEntry`) and the matching `NetworkPolicy` and
`AuthorizationPolicy` per route. Routes are split into **inbound** (gateway →
in-cluster service) and **outbound** (in-cluster → external host).

Source: `bb-common/chart/templates/routes/`, schema at
`bb-common/chart/values.schema.json:1084`.

Nothing under `routes` is emitted unless at least one named entry exists
under `routes.inbound` or `routes.outbound`.

## `routes.inbound.<name>`

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `enabled` | bool | yes | Disable an individual route without removing it. |
| `gateways[]` | list | yes | Each entry is `<namespace>/<gateway-name>`, e.g. `istio-gateway/public-ingressgateway`. |
| `hosts[]` | list | yes | Hostnames for the VirtualService. Supports Helm templating (`{{ .Values.global.domain }}`). |
| `service` | string | yes | Target Kubernetes Service name (or FQDN). Supports templating. |
| `port` | int / string | yes | Target service port. |
| `selector` | map | no | Pod labels for the generated NetworkPolicy / AuthorizationPolicy selectors (e.g. `app.kubernetes.io/name: prometheus`). |
| `resolution` | enum | no | DNS resolution for the generated inbound `ServiceEntry`. One of `DNS`, `STATIC`, `DNS_ROUND_ROBIN`, `DYNAMIC_DNS`, `NONE`. Default `DNS`. |
| `http[]` | list | no | Advanced HTTP rules (match, rewrite, retries, fault injection) merged into the VirtualService. See `tests/routes/advanced-http-rules_test.yaml`. |
| `containerPort` | int | no | Override the upstream container port (defaults to `port`). |
| `labels` / `annotations` | map | no | Applied to all generated resources for this route. |

### Resources per inbound route

| Kind | apiVersion | Name | Template |
| --- | --- | --- | --- |
| `VirtualService` | `networking.istio.io/v1` | `<name>` | `routes/inbound/_virtual-service.tpl` |
| `ServiceEntry` | `networking.istio.io/v1` | `<name>-internal` | `routes/inbound/_service-entry.tpl` |
| `NetworkPolicy` | `networking.k8s.io/v1` | `allow-ingress-to-<service>-<port>-from-ns-<gw-ns>-pod-<gw-name>` | `routes/inbound/_netpol.tpl` (only when `networkPolicies.enabled: true`) |
| `AuthorizationPolicy` | `security.istio.io/v1` | `<service>-<gw-name>-authz-policy` | `routes/inbound/_authz.tpl` (only when `istio.authorizationPolicies.enabled: true`) |

The AuthorizationPolicy restricts callers to the gateway's ServiceAccount
(`cluster.local/ns/<gw-ns>/sa/<gw-name>-ingressgateway-service-account` by
default — see `tests/routes/simple_test.yaml`).

## `routes.outbound.<name>`

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `enabled` | bool | yes | |
| `hosts[]` | list | yes | External hostnames. |
| `location` | enum | no | `MESH_EXTERNAL` (default) or `MESH_INTERNAL`. |
| `resolution` | enum | no | Same enum as inbound. Default `DNS`. |
| `ports[]` | list | yes | List of `{ number, name, protocol }`. |
| `metadata.labels` / `metadata.annotations` | map | no | Applied to generated resources. |

### Resources per outbound route

| Kind | apiVersion | Name | Template |
| --- | --- | --- | --- |
| `ServiceEntry` | `networking.istio.io/v1` | `<name>` | `routes/outbound/_service-entry.tpl` |

## Cross-section dependencies

The routes templates respect the other sections:

* `routes.inbound.<name>` only generates a `NetworkPolicy` if
  `networkPolicies.enabled: true`. The associated default policies (deny-all,
  etc.) still come from `spec.networkPolicies` — routes only add the
  gateway-permitting rule.
* `routes.inbound.<name>` only generates an `AuthorizationPolicy` if
  `istio.authorizationPolicies.enabled: true`.
* `routes.prependReleaseName: true` and `networkPolicies.prependReleaseName`
  / `istio.prependReleaseName` are independent — set each subsystem
  explicitly if you want all emitted names prefixed.

## Operator notes

* The operator does not need to special-case routes — they fall out of the
  same `helm template` pass as the rest. But `status.appliedResources`
  should record the four-resource fan-out per inbound route so prune works
  when a route is removed from `spec`.
* Test fixtures showing the full inbound resource set:
  `bb-common/chart/tests/routes/simple_test.yaml`,
  `bb-common/chart/tests/routes/values/routes.yaml`.
