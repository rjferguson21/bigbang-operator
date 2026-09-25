# Code Organization Plan

Tracking document for reorganizing `pkg/generator` and `internal/controller`.
Pure refactor: **no behavior change** — golden fixtures must pass *without*
regeneration, which is the proof the moves are mechanical.

## Go conventions this plan follows

- **A file is one concern, not one type.** Go has no class-per-file rule;
  the unit of organization is the package, and files within it should each
  tell one story (stdlib: `net/http`'s `cookie.go`, `header.go`, `status.go`).
  A 1,000-line file mixing five concerns is the main smell here.
- **Helpers live next to their primary caller.** Only when a helper is
  genuinely shared across files does it move to a deliberately *named* shared
  file (`names.go`, `metadata.go`) — never a `utils.go` dumping ground.
  (One small exception below: `util.go` holds exactly the two generic
  one-liners that have no domain home, with a comment capping its scope.)
- **Don't wrap the stdlib.** `lowercase(s)` aliasing `strings.ToLower` makes
  readers look up a definition to learn nothing. Delete trivial wrappers.
- **No dead import suppressors.** `var _ = types.NamespacedName{}` exists
  only to silence an unused import; the fix is removing the import, not
  feeding it.
- **Newspaper ordering within a file.** Exported entry points first, helpers
  after, roughly in call order — readers should be able to stop reading when
  they stop caring.
- **File names: lowercase, descriptive; underscores are fine.** The stdlib
  avoids them, but the Kubernetes ecosystem (and kubebuilder scaffolding —
  `package_controller.go`) uses them freely. We follow the ecosystem.
  `networkpolicies_defaults.go` over a cryptic `npdef.go`.
- **Split test helpers from tests when they're shared** (`suite_test.go`
  already does this correctly).
- **Generated code stays untouched and clearly marked** (`zz_generated.*`
  prefix is the k8s convention; ours already comply).

## Current state (the problems)

| File | Lines | Issue |
|---|---|---|
| `pkg/generator/networkpolicies.go` | 1,076 | Mixes 5 concerns: shorthand JSON decode types, expansion loops, k8s/cidr/literal builders, the 8 default policies + their enablement gates, and shared helpers |
| `pkg/generator/routes.go` | 397 | Hosts `sortedKeys` and `mergeMaps`, used package-wide — placement by accident of first use |
| `pkg/generator/istio.go` | 137 | Hosts `prependName` (used by every generator); has a `var _ =` import suppressor |
| `pkg/generator/authorizationpolicies.go` | 474 | Cohesive, but carries the `lowercase` stdlib wrapper |
| `pkg/generator/definitions.go` | 278 | `ls()` — too-cryptic name for a label-selector constructor |
| `pkg/generator/labels.go` | 67 | Name undersells it: it's all of metadata stamping, not just labels |
| `pkg/generator/shorthand.go` | 231 | Has the key *grammar* but not the value *decoding* (those types sit in networkpolicies.go) — the shorthand story is split across files |
| `internal/controller/package_controller.go` | 348 | Reconcile + prune machinery + status machinery in one file; two `var _ =` suppressors |

## Target layout

### `pkg/generator/`

| File | Contents |
|---|---|
| `generator.go` | `Input`, `Generate`, `Warnings`, `setGVK` — the package's front door (unchanged) |
| `istio.go` | PeerAuthentication, Sidecar, custom ServiceEntries, `mTLSMode` |
| `authorizationpolicies.go` | authz gates + the four AP passes (defaults, shorthand, routes, custom) |
| `networkpolicies.go` | `generateNetworkPolicies` entry, additionalPolicies passthrough, shared netpol helpers (`buildNetpolPorts`, `remote*Selector`, `excludeCIDRs*`, `defaultNetpolLabels`) |
| `networkpolicies_defaults.go` | the 8 default policies + per-default enablement gates (`defaultEnabled`, `egress*`/`ingress*`, `istioAmbient`) |
| `networkpolicies_shorthand.go` | `expandShorthandEgress/Ingress` + the six builders (k8s, cidr, literal × 2 directions) + `decodeLiteralRules` |
| `shorthand.go` | the complete shorthand grammar: key parsing (existing) **and** value decoding (`shorthandSource/Peer/Target`, `literalTarget`, `shorthandMetadata` + merge/apply, `flattenMatchLabels`) |
| `definitions.go` | built-in + custom definitions, definition netpol builders (`ls` → `matchLabels`) |
| `routes.go` | routes only (sheds `sortedKeys`, `mergeMaps`) |
| `hbone.go` | HBONE post-pass (unchanged) |
| `metadata.go` | (renamed from `labels.go`) label/annotation constants, `stampMetadata`, `mergeMaps`, `cloneLabels` |
| `names.go` | resource-name construction: `prependName`, `namePortSuffix`, `cidrNameSegment`, `nameAnyPod`/`cidrAnywhere` consts |
| `util.go` | `sortedKeys`, `ptr` — generic one-liners only; anything with domain meaning goes elsewhere |

### `internal/controller/`

| File | Contents |
|---|---|
| `package_controller.go` | `PackageReconciler`, RBAC markers, `Reconcile`, `applyAll`, `kubeAPIPorts`, `SetupWithManager` |
| `prune.go` | `pruneStale`, `managedListKinds`, `extractItems`, `objectKey` |
| `status.go` | `markReady`, `markFailed`, `setCondition`, `summarize` |
| `metrics.go` | unchanged |

## Checklist

### Phase 1 — shared helpers
- [x] Rename `labels.go` → `metadata.go`; move `mergeMaps`, `cloneLabels` into it
- [x] Create `names.go`: `prependName` (from istio.go), `namePortSuffix`, `cidrNameSegment` (from shorthand.go), `nameAnyPod`/`cidrAnywhere` consts (from networkpolicies.go)
- [x] Create `util.go`: `sortedKeys` (from routes.go), `ptr` (from labels.go)
- [x] Delete `lowercase` wrapper (authorizationpolicies.go) → `strings.ToLower`
- [x] Rename `ls` → `matchLabels` (definitions.go)
- [x] Remove `var _ =` import suppressors (istio.go, package_controller.go)

### Phase 2 — split networkpolicies.go
- [x] Move shorthand value-decoding types + metadata merge/apply into `shorthand.go`
- [x] Move defaults + enablement gates into `networkpolicies_defaults.go`
- [x] Move expansion loops + 6 builders + `decodeLiteralRules` into `networkpolicies_shorthand.go`
- [x] `networkpolicies.go` keeps: entry point, additionalPolicies, shared netpol helpers

### Phase 3 — controller split + verification
- [x] Move prune machinery to `prune.go`
- [x] Move status machinery to `status.go`
- [x] `go build ./...` clean
- [x] Golden tests pass **without** `UPDATE_GOLDEN` (proves zero behavior change)
- [x] envtest suite passes
- [x] `make lint` clean

## Non-goals

- No package splits (`pkg/generator` is one cohesive package; sub-packages
  would force exporting internals — Go convention is to reach for sub-packages
  only when import cycles or genuinely separate consumers demand it).
- No renames of exported API (`Input`, `Generate`, `Warnings` stay).
- No file-level churn in `api/v1alpha1` (mostly generated) or tests beyond
  what moves force.

## Outcome (2026-06-10)

All phases complete. Golden fixtures byte-identical (no regeneration needed).

| File | Before | After |
|---|---|---|
| `pkg/generator/networkpolicies.go` | 1,076 | 177 |
| `pkg/generator/networkpolicies_defaults.go` | — | 267 |
| `pkg/generator/networkpolicies_shorthand.go` | — | 474 |
| `pkg/generator/shorthand.go` | 231 | 377 (gained the value-decode types) |
| `internal/controller/package_controller.go` | 348 | 176 |
| `internal/controller/prune.go` | — | 120 |
| `internal/controller/status.go` | — | 100 |
