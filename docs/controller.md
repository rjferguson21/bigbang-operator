# Controller behavior

What the reconciler does beyond rendering manifests — the part a Helm chart
can't do.

## Reconcile flow

1. **Generate** — `pkg/generator` turns the Package spec into a deterministic
   object slice (pure function: same spec → same objects).
2. **Apply** — each object is server-side applied with field manager
   `bigbang-operator` and `ForceOwnership`.
3. **Prune** — anything carrying this package's label that the latest
   generation no longer wants is deleted.
4. **Status** — `Ready` condition, `observedGeneration`, and an
   `appliedResources` summary land on the Package.

A failure at any step short-circuits with `Ready=False` and a reason of
`GenerationFailed`, `ApplyFailed`, or `PruneFailed` (the message carries the
underlying error).

## Drift recovery

The controller `Owns()` every kind it emits — NetworkPolicy,
PeerAuthentication, AuthorizationPolicy, VirtualService, ServiceEntry,
Sidecar. Deleting or mutating a generated resource triggers a reconcile that
restores it. `kubectl edit` fights with the operator and loses
(`ForceOwnership`); change the Package spec instead.

## Pruning

Label-driven, not state-file-driven: every emitted object carries
`bigbang.dev/package: <name>`, and prune lists the six managed kinds in the
package's namespace by that label, deleting anything absent from the desired
set. Shrinking the spec (removing a route, a shorthand rule, an additional
policy) garbage-collects the corresponding resources on the next reconcile.

Deleting the Package itself cleans up via owner references — every emitted
object is owned by the Package, so apiserver GC handles teardown. No
finalizer.

## The kubeAPI Service lookup

The built-in `kubeAPI` egress definition restricts ports to the API server's
real target ports. The controller resolves them from the
`default/kubernetes` Service — the reconcile-time equivalent of bb-common's
render-time `lookup` — using the uncached API reader (one GET, no
cluster-wide Service informer), and only when a Package actually references
the definition. Lookup failure degrades to all-ports, same as bb-common when
its lookup returns nothing.

## Events

Spec fields that are accepted but deliberately not honored produce a Warning
event on the Package rather than a silent no-op:

```
Warning  UnsupportedField  networkPolicies.defaultsAsHooks is ignored: Helm hooks
                           have no operator equivalent (default policies are applied
                           and kept in sync continuously)
```

## Metrics

Served on `/metrics`:

| Metric | Type | Labels |
|---|---|---|
| `bigbang_operator_reconcile_total` | counter | `namespace`, `name`, `outcome` (`success` \| `generation_failed` \| `apply_failed` \| `prune_failed`) |
| `bigbang_operator_reconcile_duration_seconds` | histogram | `namespace`, `name` |
| `bigbang_operator_applied_resources` | gauge | `namespace`, `name` |

## RBAC

The chart's ClusterRole grants full management of the six emitted kinds plus
two narrow extras: `get` on Services (the kubeAPI lookup) and
`create`/`patch` on Events (the warnings above). RBAC source of truth is the
kubebuilder markers in `internal/controller/package_controller.go`;
`make manifests` regenerates `config/rbac/role.yaml`, and
`chart/templates/rbac.yaml` mirrors it.

## Status at a glance

```sh
$ kubectl get packages -A
NAMESPACE     NAME         READY   REASON             AGE
example-app   example-app  True    ResourcesApplied   5m
broken-app    broken-app   False   GenerationFailed   1m

$ kubectl -n broken-app get package broken-app -o jsonpath='{.status.conditions[0].message}'
routes.inbound.web: gateways[] must be non-empty when enabled
```
