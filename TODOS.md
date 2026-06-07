# TODOs

What's still left for the operator, grouped by area. Items inside each
group are roughly ordered by recommended sequence (highest first).
"Deferred from v1" markers in code and `plan/` docs feed this list.

## Reconciler & operations

- [x] **`Owns()` drift recovery** — controller now watches every emitted
  Kind, so external deletes/edits trigger reconcile.
- [x] **envtest suite for the reconciler** — three cases:
  defaults applied (apply + status), prune on spec shrink, drift recovery.
  Istio CRDs vendored under `internal/controller/testdata/crds/`. Wired
  into `make test` (77% reconciler coverage).
- [ ] **Finalizer** — only needed if/when reconcile starts doing work the
  apiserver can't GC (external system calls, ordered teardown). Skip until
  there's a concrete reason.
- [x] **Field validation for routes** — the original TODO assumed CEL on
  the `InboundRoute` struct would surface, but `routes.inbound` /
  `routes.outbound` are `x-kubernetes-preserve-unknown-fields` (because
  they're keyed maps with `patternProperties` in the bb-common schema),
  so kubebuilder validation markers there are dead. Instead, added
  reconciler-time validation in `pkg/generator/routes.go::validateInbound`
  / `validateOutbound` — required fields (`gateways[]` non-empty, valid
  `<ns>/<name>` form, `service`, `port`; outbound `hosts[]`) produce clear
  errors that surface as Ready=False / Reason=GenerationFailed on the
  Package. CEL on the typed top-level spec wasn't worth adding — the
  existing enum constraints already cover the typed surface.
- [x] **Reconcile metrics** — three series registered on the existing
  controller-runtime `/metrics` endpoint:
  `bigbang_operator_reconcile_total{namespace,name,outcome}` (counter;
  outcomes: success / generation_failed / apply_failed / prune_failed),
  `bigbang_operator_reconcile_duration_seconds{namespace,name}`
  (histogram, 10 ms…40 s exponential buckets), and
  `bigbang_operator_applied_resources{namespace,name}` (gauge of last
  successful reconcile's emitted count). Lives in
  `internal/controller/metrics.go`.

## Istio

- [x] PeerAuthentication
- [x] Default AuthorizationPolicies (`allow-nothing`, `allow-all-in-ns`)
- [x] Generated AuthorizationPolicies from ingress shorthand
- [x] **Per-route AuthorizationPolicy** — gated on
  `istio.authorizationPolicies.enabled` (or `ambient.enabled`). One AP per
  gateway per inbound route, restricting to the gateway's ServiceAccount.
  Lives in `pkg/generator/authorizationpolicies.go::generateAuthzFromRoutes`,
  golden fixture under `testdata/{inputs,golden}/with-route-authz.yaml`.
- [x] **`Sidecar` resource** — `spec.istio.sidecar.enabled` emits a
  namespace-wide Sidecar with `outboundTrafficPolicy.mode` (default
  REGISTRY_ONLY, or ALLOW_ANY). Suppressed when `ambient.enabled: true`
  (mirrors bb-common). Owned + pruned by the reconciler. Lives in
  `pkg/generator/istio.go::sidecarResource`, fixture
  `testdata/{inputs,golden}/with-sidecar.yaml`, Sidecar CRD vendored
  under `internal/controller/testdata/crds/`.
- [x] **Custom ServiceEntries / AuthorizationPolicies passthrough** —
  `istio.serviceEntries.custom[]` (list), `istio.authorizationPolicies.custom[]`
  (list), and `istio.authorizationPolicies.additionalPolicies` (map, gated
  by per-entry `enabled`). Raw `spec` is round-tripped via JSON into the
  typed proto. `Name` override on additionalPolicies entries is honored;
  `Namespace` is ignored (owner refs can't cross namespaces). Lives in
  `pkg/generator/istio.go::generateCustomServiceEntries` and
  `authorizationpolicies.go::generateCustomAuthzPolicies`. Fixture
  `testdata/{inputs,golden}/with-custom-istio.yaml`. Prune comes for free
  via the existing managed-list wiring.

## NetworkPolicies

- [x] 7 defaults + per-default disabling via `*bool`
- [x] `additionalPolicies[]` raw passthrough
- [x] Shorthand `egress.from.*` + `ingress.to.*` (k8s subkey)
- [x] **`definitions` — named subnet/port templates** referenced from
  `from.<src>.to.definition.<name>: true` (egress) and
  `to.<dst>.from.definition.<name>: true` (ingress). Built-in defaults:
  egress `kubeAPI` (note: ports omitted vs bb-common's lookup-based
  populate — override the definition to pin ports), ingress
  `gateway`/`monitoring`. Lives in `pkg/generator/definitions.go`,
  fixture `testdata/{inputs,golden}/with-definitions.yaml`.
- [x] **Shorthand `cidr` subkey** — `to.cidr.<key>` /
  `from.cidr.<key>`, parallel to `to.k8s.<key>`. Egress key:
  `[<tcp|udp>://]<cidr>[:<ports>]`; ingress key: `<cidr>` (ports come
  from local key). `0.0.0.0/0` renders as `…-anywhere`. Parsers in
  `pkg/generator/shorthand.go`, builders in `networkpolicies.go`,
  fixture `testdata/{inputs,golden}/with-cidr-shorthand.yaml`.
- [ ] **`defaultsAsHooks`** — emit `-as-hook` copies of each default for
  Helm hook phases (`pre-install`, `pre-upgrade`, `post-delete`).
  Only matters if a Helm-style install model returns.
- [x] **`hbonePortInjection`** — post-pass that appends TCP/15008 to every
  egress/ingress rule that has explicit ports AND at least one
  namespaceSelector/podSelector peer. Auto-on when `istio.ambient.enabled:
  true`; explicit `networkPolicies.hbonePortInjection.enabled` flag is the
  non-ambient override. Mutated policies are labeled
  `ambient.istio.network-policies.bigbang.dev/hbone-injected=true`. Lives
  in `pkg/generator/hbone.go`, fixture
  `testdata/{inputs,golden}/with-hbone-injection.yaml`.
- [x] **`excludeCIDRs`** — applied to egress `cidr` shorthand rules.
  Default `["169.254.169.254/32"]`; each exclusion goes into
  `ipBlock.except` only when strictly contained in the rule's CIDR.
  Implemented as `applyExcludeCIDRs` in `pkg/generator/networkpolicies.go`
  using `net.ParseCIDR` (much simpler than bb-common's Helm version).
  Fixture `testdata/{inputs,golden}/with-exclude-cidrs.yaml`.
- [ ] **De-duplication** — bb-common dedupes identical specs and suffixes
  collisions with `-deduped-N`. Operator currently skips dedup; add only
  if real packages hit duplicates.

## Routes

- [x] Inbound: VirtualService + ServiceEntry + gateway-permitting NetworkPolicy
- [x] Outbound: ServiceEntry
- [x] **Advanced HTTP rules** — `routes.inbound.<n>.http[]` accepts raw
  Istio HTTPRoute entries (match/rewrite/retries/fault/etc) and replaces
  the simple single-destination default. Each entry is JSON-round-tripped
  through the istio proto so SSA marshals it correctly; istiod validates
  the spec at apply time. Lives in `pkg/generator/routes.go::buildHTTPRoutes`,
  fixture `testdata/{inputs,golden}/with-advanced-http.yaml`. Type added
  as `Http []apiextensionsv1.JSON` on `InboundRoute` (the field is opaque
  in the CRD since the routes map is preserve-unknown-fields anyway).
- [x] **`prependReleaseName`** — `routes.prependReleaseName: true` now
  prepends `<package-name>-` to VirtualService, inbound ServiceEntry,
  gateway-permitting NetworkPolicy, outbound ServiceEntry, and per-route
  AuthorizationPolicy names. Independent of the `istio` /
  `networkPolicies` flags. Fixture
  `testdata/{inputs,golden}/with-routes-prepended.yaml`.
- [x] **Per-route AuthorizationPolicy** — also listed under *Istio* above.

## Tests

- [x] Generator golden tests (6 fixtures)
- [x] **envtest reconciler suite** — also listed under *Reconciler*.
- [x] **Kind/k3d e2e** — `test/e2e/bb_smoke.sh` drives 5 scenarios
  (defaults, prune on shrink, routes + per-route AP, CIDR + default
  excludeCIDRs, Sidecar emit/prune) against whatever cluster the operator
  is deployed to. `make bb-smoke` wraps it. Each test uses an isolated
  namespace and waits on the Package's Ready condition before asserting.
  Extend by appending another `test_*` function.
- [ ] **Side-by-side fixtures vs bb-common** — `helm template
  /home/rob/bb/bb-common/chart -f full-api-values.yaml` against the
  operator's output for the same `Package`. Documents intentional
  divergences and catches accidental ones.

## Codegen & schema

- [x] `*bool` promotion for `*Defaults*` structs + `GenerateFromNetpol`
- [ ] **Typed shorthand source/target structs in `api/v1alpha1`** — today
  the generator JSON-unmarshals `apiextensionsv1.JSON` values into local
  structs declared in `pkg/generator/networkpolicies.go`. Promoting these
  to the API package would let the CRD validate them properly.
- [ ] **CRD-level required fields / cross-field validation** — currently
  loose. Tighten where it's clearly safe (e.g. require `service` and
  `port` on an enabled inbound route).

## Distribution

- [x] Big Bang Helm chart in `chart/` with deployment, RBAC, CRD sync
- [x] Local dev `make` targets + Kyverno PolicyException for k3d
- [x] **Basic CI** — `.github/workflows/{test,lint,test-e2e}.yml` running
  on push to main + all PRs. `test.yml` caches `bin/` (envtest control
  plane), runs `make test`, checks generated artifacts for drift, and
  builds the manager binary. Lint runs golangci-lint v2.5.0 with `pkg/*`
  excluded from `lll`/`prealloc` (long generator signatures are inherent).
  All three workflows green on main; first run ~5 min, cached run ~1 min.
- [ ] **Iron Bank image** — replace the local `:dev` flow with an
  immutable tag from `registry1.dso.mil`. Removes the need for the
  PolicyException in production.
- [ ] **Tighten chart RBAC** — current ClusterRole grants
  `get;list;watch;create;update;patch;delete` on all managed Kinds across
  the cluster. Consider a per-namespace Role for the operator's own
  artifacts + a narrow ClusterRole for what must be cluster-wide.
- [ ] **Decide global config knob** (open item in TechnicalPlan §11) —
  cluster-wide defaults for `domain`, registry, etc.
- [ ] **Purpose-built k3d cluster bootstrap** — `make dev-bbcluster`
  currently `cd`s into `/home/rob/bb/bigbang` and shells out to
  `bbtask default DISABLE_CORE=true`. That's brittle: the BB taskfile's
  top-level `PACKAGE_PATH` var eagerly reverse-looks-up the cwd's git
  origin against `bigbang/chart/values.yaml`, so the operator repo (not
  in the umbrella) can't host the invocation. Replace with a self-contained
  bootstrap — likely a `hack/local-dev/` script that calls the bb-k3d
  chart + `helm install bigbang` directly with the same `disable-core`
  values — so the operator owns its dev-cluster recipe end-to-end.

## bb-common parity gaps (audit 2026-06-07)

Found in a side-by-side review of `bb-common/chart/templates`. Roughly
ordered by impact — top items are user-facing or correctness-affecting,
bottom items are niche.

- [x] **Inbound route `selector` inference** — when omitted, defaults to
  `{app.kubernetes.io/name: <route-name>}` before validation runs.
- [x] **`allow-ambient-kubelet` default ingress** — emitted when
  `istio.ambient.enabled: true`; allows ingress from `169.254.7.127/32`.
  Also fixed: `allow-prometheus-to-istio-sidecar` is now suppressed
  under ambient (matches bb-common's gating).
- [x] **Outbound ServiceEntry naming `-external` / `-internal`** —
  `buildOutboundServiceEntry` now suffixes by location.
- [x] **Outbound SE default port protocol** — flipped `TLS` → `HTTPS`
  to match bb-common.
- [ ] **Helm-style templating in `routes.<…>.hosts[]`** — bb-common runs
  each host through `tpl` so users can write
  `podinfo.{{ .Values.global.domain }}`. The operator has no
  `global.domain` and no template engine. **Deferred by decision
  (2026-06-07)** — package CRs need literal hostnames for now; users
  needing dynamic values can substitute via GitOps/kustomize before
  apply. If revisited, the recommended shape is: operator-level config
  (chart values → env vars → in-memory map) + plain `strings.ReplaceAll`
  substitution on `routes.hosts[]` only, mimicking the bb-common syntax
  (`{{ .Values.global.domain }}`) so values files copy-paste cleanly.
  `configRef` (per-Package CM lookup) is a richer alternative if
  multi-tenant per-namespace domains ever matter. See conversation
  history for the full tradeoff analysis.
- [x] **AP generation from CIDR shorthand** — `buildAuthzFromCIDR` emits
  one AP per enabled CIDR ingress rule using `ipBlocks` source. Mirrors
  the K8s shorthand's `with-identity` flow. Fixture `with-cidr-authz.yaml`.
- [x] **Routes `passthrough` mode** — `routes.inbound.<name>.passthrough.enabled`
  switches the VirtualService from `spec.http[]` to `spec.tls[]` with an
  SNI match on `passthrough.gatewayPort` (default 8443). New
  `RoutePassthrough` type in `api/v1alpha1`. Fixture `with-passthrough.yaml`.
- [ ] **Route gateway pod-selector includes `istio: ingressgateway`** —
  bb-common's inbound netpol uses
  `{app.kubernetes.io/name: <gw>, istio: ingressgateway}`. Ours only uses
  `app.kubernetes.io/name`. Functionally equivalent on real BB gateways
  (both labels present), but a stricter selector is closer to the source.
- [ ] **`metadata-overrides` on shorthand netpols** — bb-common merges
  per-rule `metadata.{labels,annotations}` from both the local and remote
  shorthand entries. Niche; raise only if a real package needs it.
- [ ] **`from-spec-literal` egress rule** — raw NetworkPolicy egress
  spec under `to.<key>.spec`. `additionalPolicies[]` covers the same need
  with cleaner ergonomics; deferred.

## Out of v1 (no action planned)

- Workload Helm release (`spec.source`, `spec.version`, `spec.values`).
- Cross-package dependencies / ordering.
- Flux integration (HelmRelease / Kustomization).
- Multi-cluster fan-out.
