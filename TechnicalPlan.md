# TechnicalPlan.md

Detailed counterpart to [Plan.md](Plan.md). Describes the v1 implementation of
the Big Bang Package operator: how the `Package` CRD is structured, how the
controller generates and reconciles resources, and how the project is built and
shipped.

## 1. Scope

In scope for v1:

* One CRD: `Package` (`bigbang.dev/v1alpha1`, **namespaced**).
* Native Go generation of the resource set that bb-common produces for
  `spec.istio`, `spec.networkPolicies`, and `spec.routes`. No Helm at runtime;
  no bb-common chart embedded in the operator.
* Server-side apply with field manager `bigbang-operator`, owner-label-based
  prune, status updates.

Out of scope (matches Plan.md):

* Workload Helm release (`spec.source`, `spec.version`, `spec.values`
  reserved but unused).
* Cross-package dependencies, Flux integration, multi-cluster.
* Webhooks, conversion, drift auto-recovery on owned objects.

## 2. Architecture

```text
┌──────────────┐  watch  ┌──────────────────┐  SSA   ┌────────────────┐
│ Package CR   │ ─────▶  │ PackageReconciler│ ─────▶ │ K8s API server │
└──────────────┘         └────────┬─────────┘        └────────────────┘
                                  │ uses
                                  ▼
                         ┌──────────────────┐
                         │ pkg/generator    │  pure Go: spec → []client.Object
                         └──────────────────┘
```

The reconciler is intentionally thin. All resource-shaping logic lives in
`pkg/generator`, which is pure-Go and unit-testable without an apiserver.

## 3. Repo layout

Standard kubebuilder layout, plus an internal generator package:

```text
.
├── cmd/main.go
├── api/v1alpha1/
│   ├── package_types.go        # hand-curated wrapper (TypeMeta, Spec, Status)
│   ├── zz_generated.types.go   # codegen output (see §4)
│   ├── zz_generated.deepcopy.go
│   └── groupversion_info.go
├── internal/controller/
│   ├── package_controller.go
│   └── package_controller_test.go
├── pkg/generator/              # spec → []client.Object
│   ├── istio.go
│   ├── networkpolicies.go
│   ├── routes.go
│   ├── labels.go               # shared label/annotation helpers
│   └── *_test.go               # unit tests with golden YAML fixtures
├── pkg/generator/testdata/
│   └── golden/                 # YAML files generated tests assert against
├── chart/                      # Big Bang package chart (see §10)
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── values.schema.json
│   └── templates/
├── config/                     # kubebuilder kustomize bases (CRDs, RBAC, mgr)
├── hack/
│   └── schema-to-go.sh         # codegen wrapper (see §4)
├── Plan.md
├── TechnicalPlan.md
└── plan/                       # supporting plan docs
```

`api/` and `config/` are scaffolded by `kubebuilder init` + `kubebuilder
create api --group bigbang --version v1alpha1 --kind Package --namespaced`.

## 4. API types and codegen

### Source of truth

`bb-common/chart/values.schema.json` (JSON Schema draft 2020-12, ~1500 lines)
is the behavioral spec for `Package.spec`. The operator is free to diverge
cosmetically from bb-common's output (label ordering, omitted Helm hook
annotations, etc.) — bb-common is the *spec*, not a golden oracle.

### Generation pipeline

1. **Tool**: [`go-jsonschema`](https://github.com/omissis/go-jsonschema)
   (atombender) — generates idiomatic Go structs from JSON Schema.
2. **Wrapper script**: `hack/schema-to-go.sh` runs go-jsonschema against
   `bb-common/chart/values.schema.json` and writes
   `api/v1alpha1/zz_generated.types.go`.
3. **Hand-curated `package_types.go`** defines `Package`, `PackageSpec`,
   `PackageStatus`, with kubebuilder markers:

   ```go
   // +kubebuilder:object:root=true
   // +kubebuilder:subresource:status
   type Package struct {
       metav1.TypeMeta   `json:",inline"`
       metav1.ObjectMeta `json:"metadata,omitempty"`
       Spec   PackageSpec   `json:"spec,omitempty"`
       Status PackageStatus `json:"status,omitempty"`
   }

   type PackageSpec struct {
       Istio           *IstioSpec           `json:"istio,omitempty"`
       NetworkPolicies *NetworkPoliciesSpec `json:"networkPolicies,omitempty"`
       Routes          *RoutesSpec          `json:"routes,omitempty"`
   }
   ```

   `IstioSpec`, `NetworkPoliciesSpec`, `RoutesSpec` come from the generated
   file. Hand-tuning is limited to adding `+kubebuilder:validation:*` markers
   and `omitempty`.
4. **`controller-gen`** then produces:
   * `zz_generated.deepcopy.go`
   * `config/crd/bases/bigbang.dev_packages.yaml` (the CRD manifest, with
     OpenAPI schema and CEL validations baked in).

### Versioning

The operator binary version *is* the bb-common API version. The schema is
vendored at build time; users do not pick a bb-common version per Package.
Schema bumps ship as new operator releases.

## 5. Validation

CRD-level only. No webhook server in v1.

* OpenAPI validation comes from the JSON Schema (translated through the
  kubebuilder markers on generated types).
* Cross-field rules use `+kubebuilder:validation:XValidation` (CEL) where
  needed — for example, requiring `gateways[]` non-empty when an inbound
  route is `enabled: true`.
* Anything the controller can't honor surfaces as a `Ready=False` condition,
  not an admission rejection.

## 6. Resource generator (`pkg/generator`)

The heart of v1. Public surface:

```go
type Input struct {
    Package   *bbv1alpha1.Package // namespaced; provides Name/Namespace
    Scheme    *runtime.Scheme
}

// Generate returns the full set of objects to apply for one Package.
// Pure function: same Input → same []client.Object.
func Generate(in Input) ([]client.Object, error)
```

Implementation is split per subsystem (`istio.go`, `networkpolicies.go`,
`routes.go`), matching the Plan docs. Each subsystem function returns its
contribution to the slice. `Generate` concatenates and stamps shared
metadata.

### Shared object metadata

Every generated object carries:

| Label / annotation | Value | Purpose |
| --- | --- | --- |
| `app.kubernetes.io/managed-by` | `bigbang-operator` | Identification |
| `bigbang.dev/package` | `<package-name>` | **Owner-label prune key (§7)** |
| `network-policies.bigbang.dev/source` | `bb-common` (only on NetworkPolicies) | Matches Plan/default.md |
| ownerReference | the `Package` CR | GC on package delete |

`bigbang.dev/package` is the prune key — never mutate it, never apply
two Packages with the same name (namespace-scoped CRD prevents this).

### Subsystem responsibilities (concrete)

* **`istio.go`** — emits `PeerAuthentication/default-peer-auth` when
  `spec.istio.enabled`; optional `Sidecar`; default + generated
  `AuthorizationPolicy` per `plan/istio.md`. Passes through
  `serviceEntries.custom[]`, `authorizationPolicies.custom[]`,
  `authorizationPolicies.additionalPolicies` verbatim.
* **`networkpolicies.go`** — emits the 8 default policies per
  `plan/networkPolicies.md`; expands shorthand `egress.from.*` and
  `ingress.to.*` into named NetworkPolicies; spreads `additionalPolicies[]`
  through unchanged. Naming rules and de-dup logic are reimplemented from
  bb-common's templates.
* **`routes.go`** — per inbound route emits `VirtualService`,
  `ServiceEntry`, `NetworkPolicy` (gated on `networkPolicies.enabled`),
  `AuthorizationPolicy` (gated on `istio.authorizationPolicies.enabled`).
  Per outbound route emits `ServiceEntry`.

### Object construction

Use typed API objects where available — `networkingv1.NetworkPolicy` and
`networkingv1beta1`/`v1` from `k8s.io/api`. For Istio CRDs, depend on
`istio.io/api` typed Go bindings (`networking/v1`, `security/v1`). Avoids
hand-built `unstructured.Unstructured` and keeps tests readable.

## 7. Reconciler

`internal/controller/package_controller.go` is the only controller.

```go
func (r *PackageReconciler) Reconcile(ctx, req) (Result, error) {
    pkg := &bbv1alpha1.Package{}
    if err := r.Get(ctx, req.NamespacedName, pkg); err != nil { return ... }
    if !pkg.DeletionTimestamp.IsZero() { return r.handleDelete(ctx, pkg) }

    desired, err := generator.Generate(generator.Input{Package: pkg, Scheme: r.Scheme})
    if err != nil { return r.markFailed(ctx, pkg, err) }

    if err := r.applyAll(ctx, pkg, desired); err != nil { ... }
    if err := r.pruneStale(ctx, pkg, desired); err != nil { ... }

    return r.markReady(ctx, pkg, desired)
}
```

### Watch model

`SetupWithManager` watches **`Package` only** — no `Owns()` on the
emitted Kinds. Trade-off: manual edits to a generated NetworkPolicy
won't be undone until the next Package update. Acceptable for v1; revisit
when drift becomes a real complaint.

### Apply

Server-side apply with `FieldOwner("bigbang-operator")` and
`client.ForceOwnership`. Each object goes through `r.Patch(ctx, obj,
client.Apply, ...)`. SSA conflicts (another field manager owning the
field) surface as a `Ready=False` condition rather than silent overwrite.

### Prune (SSA + owner-label sweep)

For each Kind the generator can emit, on each reconcile:

1. `r.List(ctx, &kindList, client.InNamespace(pkg.Namespace),
   client.MatchingLabels{"bigbang.dev/package": pkg.Name})`.
2. Compute `desiredSet := {GVK,Name} for o in desired}`.
3. Delete anything in the list not in `desiredSet`.

The Kinds-to-list set is hard-coded (it's exactly the Kinds the generator
can produce): `PeerAuthentication`, `Sidecar`, `AuthorizationPolicy`,
`ServiceEntry`, `VirtualService`, `NetworkPolicy`. Adding a Kind to the
generator means adding it to the sweep list.

This avoids growing `status.appliedResources` indefinitely (Plan.md's
original sketch) and survives a controller restart — the label is the
durable record, not status.

### Delete

Owner references on every applied object handle garbage collection when
the `Package` is deleted; no finalizer needed in v1. (If we later need to
do cleanup the apiserver won't — e.g., calling out to an external system
— add a `bigbang.dev/finalizer`.)

## 8. Status

```go
type PackageStatus struct {
    ObservedGeneration int64               `json:"observedGeneration,omitempty"`
    Conditions         []metav1.Condition  `json:"conditions,omitempty"`
    AppliedResources   []AppliedResource   `json:"appliedResources,omitempty"`
}

type AppliedResource struct {
    APIVersion string `json:"apiVersion"`
    Kind       string `json:"kind"`
    Name       string `json:"name"`
}
```

* `Conditions` follows the Kubernetes condition convention.
  `Ready` = `True` on a clean apply, `False` with a reason for any error
  (`GenerationFailed`, `ApplyFailed`, `PruneFailed`).
* `AppliedResources` is informational only — prune is label-driven, so
  the list is for human/dashboard consumption.
* `ObservedGeneration` is set to `pkg.Generation` at the end of a
  successful reconcile.

## 9. Tests

v1 ships with **unit tests on `pkg/generator`** only.

* Table-driven: each test row has an input `Package` (Go struct or YAML
  loaded via sigs.k8s.io/yaml) and a golden YAML fixture under
  `pkg/generator/testdata/golden/`.
* `Generate(in)` is marshaled to YAML and compared line-for-line against
  the fixture; mismatches print a unified diff. `UPDATE_GOLDEN=1 go test`
  rewrites the fixtures.
* Fixture coverage mirrors the cases called out in the plan docs:
  defaults-only, shorthand egress/ingress, raw `additionalPolicies[]`,
  inbound + outbound routes, ambient mode, `prependReleaseName`.

envtest and Kind-based e2e are explicitly **deferred** to a later
revision. The controller is thin enough that exercising it through
golden generator tests covers the v1 surface area.

## 10. Distribution

Operator ships as a **Big Bang package**: a Helm chart in `chart/` that
deploys the controller. The chart has the shape of any other Big Bang
package and will be referenced from the umbrella with a HelmRelease.

* `chart/templates/` contains the operator Deployment, ServiceAccount,
  ClusterRole/ClusterRoleBinding (per the RBAC markers in the
  controller), and the `Package` CRD (imported from
  `config/crd/bases/`).
* `chart/values.yaml` covers image tag, replicas, resources, log level.
* The kubebuilder-generated `config/` kustomize bundle stays as the
  source of truth for the Deployment + RBAC; a small `hack/` script
  copies the rendered manifests into `chart/templates/` so we don't
  hand-maintain two copies.

`make` targets (provided by kubebuilder + a few additions):

| Target | Purpose |
| --- | --- |
| `make generate` | controller-gen deepcopy + CRD manifests |
| `make manifests` | regenerate `config/crd` and `config/rbac` |
| `make schema-to-go` | run `hack/schema-to-go.sh` (regenerate Go types from bb-common schema) |
| `make test` | `go test ./...` |
| `make docker-build` / `make docker-push` | build/push the operator image |
| `make chart-sync` | mirror `config/` into `chart/templates/` |

## 11. Open items

These don't block v1 but should be settled before tagging a release:

* **Image registry / coordinates** for the operator image (Iron Bank?
  Registry1?).
* **RBAC granularity** — start with a single ClusterRole covering the six
  generated Kinds plus `Package`; revisit if customers ask for namespaced
  install.
* **Metrics** — kubebuilder ships a default `/metrics`. Decide whether to
  add reconcile counters / generation latency histograms in v1 or wait.
* **Conformance test fixtures** — even though we're not bound to
  bb-common output, dropping a few side-by-side YAML examples in
  `plan/` is cheap insurance for users migrating from bb-common.
