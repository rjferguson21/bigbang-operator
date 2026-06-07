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
- [ ] **CEL validations on the CRD** — e.g. require `gateways[]` non-empty
  when an inbound route is `enabled: true`. Lives in kubebuilder markers on
  `api/v1alpha1/routes_shorthand.go`.
- [ ] **Reconcile metrics** — counters for success/failure, histograms for
  reconcile latency. Kubebuilder ships `/metrics`; just register custom
  counters in `package_controller.go`.

## Istio

- [x] PeerAuthentication
- [x] Default AuthorizationPolicies (`allow-nothing`, `allow-all-in-ns`)
- [x] Generated AuthorizationPolicies from ingress shorthand
- [ ] **Per-route AuthorizationPolicy** — gated on
  `istio.authorizationPolicies.enabled`. Restricts to the gateway's
  ServiceAccount (`cluster.local/ns/<gw-ns>/sa/<gw-name>-ingressgateway-service-account`).
  Adds AP per inbound route to `pkg/generator/routes.go`.
- [ ] **`Sidecar` resource** — `spec.istio.sidecar.enabled` emits a
  namespace-wide Sidecar with `outboundTrafficPolicy.mode`.
- [ ] **Custom ServiceEntries / AuthorizationPolicies passthrough** —
  `istio.serviceEntries.custom[]`,
  `istio.authorizationPolicies.custom[]`,
  `istio.authorizationPolicies.additionalPolicies` (map form).

## NetworkPolicies

- [x] 7 defaults + per-default disabling via `*bool`
- [x] `additionalPolicies[]` raw passthrough
- [x] Shorthand `egress.from.*` + `ingress.to.*` (k8s subkey)
- [ ] **`definitions` — named subnet/port templates** referenced from
  `from.<src>.to.definition.<name>: true`. bb-common feature; both
  loki and kiali HRs use it heavily.
- [ ] **Shorthand `cidr` subkey** — `to.cidr.<key>` /
  `from.cidr.<key>`, parallel to `to.k8s.<key>`.
- [ ] **`defaultsAsHooks`** — emit `-as-hook` copies of each default for
  Helm hook phases (`pre-install`, `pre-upgrade`, `post-delete`).
  Only matters if a Helm-style install model returns.
- [ ] **`hbonePortInjection`** — inject port 15008 into rules under ambient
  mode (auto-on when `istio.ambient.enabled: true`).
- [ ] **`excludeCIDRs`** — strip configured CIDRs from all ipBlock egress
  rules (default `169.254.169.254/32`).
- [ ] **De-duplication** — bb-common dedupes identical specs and suffixes
  collisions with `-deduped-N`. Operator currently skips dedup; add only
  if real packages hit duplicates.

## Routes

- [x] Inbound: VirtualService + ServiceEntry + gateway-permitting NetworkPolicy
- [x] Outbound: ServiceEntry
- [ ] **Advanced HTTP rules** — `http[]` array on inbound routes (match,
  rewrite, retries, fault injection) merged into the VirtualService.
- [ ] **`prependReleaseName`** — name rewriting for route-emitted resources.
  `istio` and `networkPolicies` have their own independent flag; route
  generation should honor `routes.prependReleaseName` too.
- [ ] **Per-route AuthorizationPolicy** — also listed under *Istio* above.

## Tests

- [x] Generator golden tests (6 fixtures)
- [x] **envtest reconciler suite** — also listed under *Reconciler*.
- [ ] **Kind/k3d e2e** — driven by `make dev-deploy` + a Bash test
  harness; assert reconciler emits expected resources and prunes on spec
  change. Could live under `test/e2e/`.
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
- [ ] **Iron Bank image** — replace the local `:dev` flow with an
  immutable tag from `registry1.dso.mil`. Removes the need for the
  PolicyException in production.
- [ ] **Tighten chart RBAC** — current ClusterRole grants
  `get;list;watch;create;update;patch;delete` on all managed Kinds across
  the cluster. Consider a per-namespace Role for the operator's own
  artifacts + a narrow ClusterRole for what must be cluster-wide.
- [ ] **Decide global config knob** (open item in TechnicalPlan §11) —
  cluster-wide defaults for `domain`, registry, etc.

## Out of v1 (no action planned)

- Workload Helm release (`spec.source`, `spec.version`, `spec.values`).
- Cross-package dependencies / ordering.
- Flux integration (HelmRelease / Kustomization).
- Multi-cluster fan-out.
