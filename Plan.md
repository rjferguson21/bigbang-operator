* An operator built with KubeBuilder that reconciles Big Bang `Package` CRs
  into the istio + NetworkPolicy resources bb-common would render.
* Resource generation is pure-Go in `pkg/generator` — no Helm at runtime, no
  bb-common chart embedded. The schema lives in `api/v1alpha1` and is
  regenerated from `bb-common/chart/values.schema.json` via
  `hack/schema-to-go.sh`.
* This Plan.md is the high-level summary (<100 lines). See
  [TechnicalPlan.md](TechnicalPlan.md) for the detailed design.

## What it does

For each `Package` CR the controller:

1. Generates the desired `[]client.Object` natively in Go from `spec`.
2. Stamps every object with an owner reference back to the `Package`,
   `bigbang.dev/package=<name>` (the prune key), and
   `app.kubernetes.io/managed-by=bigbang-operator`.
3. Server-side applies each object with field manager `bigbang-operator`
   and `client.ForceOwnership`.
4. Prunes by listing each managed Kind with the
   `bigbang.dev/package=<name>` label and deleting anything not in the
   current render.
5. Updates `status` — `Ready` condition with reason/message,
   `observedGeneration`, and an informational `appliedResources` list.

The reconciler watches `Package` only — no `Owns()` on emitted Kinds; manual
edits to a generated resource won't be reverted until the next Package change.

## What gets emitted (v1 scope)

`Package.spec` mirrors bb-common's values. Documented per-subsystem:

* [`spec.istio`](plan/istio.md) — `PeerAuthentication/default-peer-auth` with
  configurable mTLS mode.
  *Deferred:* `Sidecar`, default + generated `AuthorizationPolicy`, custom
  ServiceEntry / authz passthrough.
* [`spec.networkPolicies`](plan/networkPolicies.md) — 7 default policies
  (4 egress + 3 ingress; istiod skipped in ambient), and raw
  `additionalPolicies[]` passthrough.
  *Deferred:* shorthand `egress.from.*` / `ingress.to.*` expansion,
  `definitions`, per-default disabling (needs `*bool` markers from
  schema-to-go), `defaultsAsHooks`.
* [`spec.routes`](plan/routes.md) — per inbound route a `VirtualService`,
  paired `ServiceEntry` (`<name>-internal`, `MESH_INTERNAL`), and a
  gateway-permitting `NetworkPolicy` per gateway (when
  `networkPolicies.enabled`). Per outbound route a `ServiceEntry`
  (`MESH_EXTERNAL`, default `DNS` resolution).
  *Deferred:* per-route `AuthorizationPolicy`, advanced HTTP rules,
  `prependReleaseName` rewriting.

Managed Kinds the reconciler sweeps for prune:
`NetworkPolicy`, `PeerAuthentication`, `VirtualService`, `ServiceEntry`.

## Out of scope for v1

* The Helm release for the package's workload chart (`spec.source`,
  `spec.version`, `spec.values` are reserved but unused).
* Cross-package dependencies / ordering.
* HelmRelease / Kustomization integration (Flux).
* Multi-cluster fan-out.
* envtest / e2e tests — v1 ships with `pkg/generator` golden tests only.

## Reference

* Example `Package`: [plan/example-package.yaml](plan/example-package.yaml)
* Default resources per `Package`: [plan/default.md](plan/default.md)
* Testing bb-common rendering: [plan/testing-bb-common.md](plan/testing-bb-common.md)
* Live samples translated from cluster HelmReleases:
  [`config/samples/`](config/samples/) — `loki_package.yaml`,
  `kiali_package.yaml` (each labels supported vs deferred fields inline).

## Local development

Inner loop (out-of-cluster controller, ~2s reload):

```sh
make install   # apply the CRD once
make run       # manager runs against ~/.kube/config
kubectl apply -f config/samples/bigbang_v1alpha1_package.yaml
```

Full-cluster validation against the `bb-helm` k3d cluster:

```sh
make dev-deploy     # docker-build → k3d import → chart-sync → polex → helm upgrade → restart
make dev-logs       # tail manager logs
make dev-undeploy   # uninstall + delete polex + delete namespace
```

`dev-deploy` also applies a Kyverno `PolicyException`
(`hack/local-dev/policy-exception.yaml`) so the locally-built `:dev` tag
is allowed in the `bigbang-operator` namespace. The exception lives in the
`kyverno` namespace because this cluster's Kyverno is started with
`--exceptionNamespace=kyverno`.
