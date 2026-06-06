# `spec.istio`

bb-common keys under `istio` configure Istio-specific resources. The operator
passes them through to `helm template` and applies whatever bb-common renders.

Source: `bb-common/chart/templates/istio/`, schema at
`bb-common/chart/values.schema.json`.

## Fields

| Field | Type | Default | Effect |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Master switch. Nothing under `istio` is emitted unless `true` (or `ambient.enabled: true`). |
| `prependReleaseName` | bool | `false` | Prepend release name to all emitted istio resource names. |
| `ambient.enabled` | bool | `false` | Treat the namespace as Istio ambient. Suppresses sidecar-only defaults (`allow-istiod` egress netpol) and forces default AuthorizationPolicies on. |
| `mtls.mode` | enum | `STRICT` | One of `STRICT`, `PERMISSIVE`, `DISABLE`. Sets `spec.mtls.mode` on the `PeerAuthentication`. |
| `sidecar.enabled` | bool | `false` | Emit a namespace-wide `Sidecar` resource restricting outbound traffic. |
| `sidecar.outboundTrafficPolicyMode` | enum | `REGISTRY_ONLY` | `REGISTRY_ONLY` or `ALLOW_ANY`. |
| `serviceEntries.custom[]` | list | `[]` | Raw `ServiceEntry` passthrough (name, labels, annotations, spec). |
| `authorizationPolicies.enabled` | bool | `false` | Turn on default + generated `AuthorizationPolicy` resources. |
| `authorizationPolicies.generateFromNetpol` | bool | `true` | When on, one `AuthorizationPolicy` is created per ingress NetworkPolicy mirroring its sources. |
| `authorizationPolicies.defaults.enabled` | bool | `true` | Gate the two default policies below. |
| `authorizationPolicies.defaults.denyAll.enabled` | bool | `true` | `default-authz-allow-nothing`. |
| `authorizationPolicies.defaults.allowInNamespace.enabled` | bool | `true` | `default-authz-allow-all-in-ns`. |
| `authorizationPolicies.custom[]` | list | `[]` | Raw `AuthorizationPolicy` passthrough (list form). |
| `authorizationPolicies.additionalPolicies` | map | `{}` | Raw `AuthorizationPolicy` passthrough (map form, keyed by name). |

## Resources emitted

| Trigger | Kind / Name | Template |
| --- | --- | --- |
| `enabled: true` | `PeerAuthentication/default-peer-auth` | `istio/defaults/_peer-auth.tpl` |
| `sidecar.enabled: true` | `Sidecar/sidecar` | `istio/defaults/_sidecar.tpl` |
| `authorizationPolicies.enabled: true` (or `ambient.enabled: true`), `defaults.enabled: true`, `defaults.denyAll.enabled: true`, ingress netpol defaults on | `AuthorizationPolicy/default-authz-allow-nothing` | `istio/authorization-policies/defaults/_allow-nothing.tpl` |
| same as above + `defaults.allowInNamespace.enabled: true` | `AuthorizationPolicy/default-authz-allow-all-in-ns` | `istio/authorization-policies/defaults/_allow-all-in-ns.tpl` |
| `authorizationPolicies.enabled: true` + `generateFromNetpol: true` | 1 × `AuthorizationPolicy` per ingress `NetworkPolicy` | `istio/authorization-policies/generate/` |
| `serviceEntries.custom[]` | 1 × `ServiceEntry` each | `istio/custom/_service-entries.tpl` |
| `authorizationPolicies.custom[]`, `authorizationPolicies.additionalPolicies` | 1 × `AuthorizationPolicy` each | `istio/custom/_authorization-policies.tpl`, `istio/authorization-policies/_additional.tpl` |

## Notes

* `ambient.enabled: true` enables default AuthorizationPolicies even when
  `authorizationPolicies.enabled: false` (per
  `tests/istio/authorization-policies-defaults_test.yaml`).
* Default AuthorizationPolicies are also gated by
  `networkPolicies.ingress.defaults.enabled` — if ingress defaults are off,
  the corresponding authz defaults are suppressed.
* `prependReleaseName` only affects emitted names; references inside specs
  (e.g. ServiceEntry hosts) are not rewritten.
