# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.2] (2026-10-10)

### Fixed

- The published image is now a multi-arch manifest (`linux/amd64` and
  `linux/arm64`), so the operator runs on arm64 clusters such as k3d on
  Apple Silicon (#10)

## [0.3.1] (2026-10-09)

### Fixed

- Default AuthorizationPolicies now honor `istio.prependReleaseName`, so two
  Packages in one namespace no longer fight over the same objects in a hot
  reconcile loop (#8)

## [0.3.0] (2026-10-09)

### Added

- Shared egress/ingress definitions: a global ConfigMap
  (`bigbang-operator-global`, `egressDefinitions` / `ingressDefinitions`
  keys) whose definitions every Package can reference via
  `networkPolicies.*.definition.<name>`; precedence is
  built-in < shared < package-local, and ConfigMap edits re-reconcile all
  Packages

## [0.2.1] (2026-09-30)

### Fixed

- Explicit empty `namespaceSelector: {}` / `podSelector: {}` in network policy
  definitions were dropped, silently narrowing any-namespace rules to
  same-namespace-only (#1)
- Inbound route NetworkPolicy/AuthorizationPolicy now allow the workload port
  (`containerPort`, falling back to `port`) instead of the service port, so
  routes like grafana's 80→3000 admit the traffic they declare (#2)

## [0.2.0] (2026-09-28)

### Added

- kstatus conformance: `Reconciling` and `Stalled` conditions on the Package
  CR, so kstatus-based tooling (cli-utils, Flux health checks, ArgoCD)
  correctly computes InProgress/Failed/Current
- Auto-tag workflow: a chart version bump on main creates the release tag
  and dispatches the release workflow

### Changed

- `status.observedGeneration` is now stamped on failed reconciles too — it
  means "generation processed", not "applied successfully"
- Reorganized generator/controller packages; added docs and LICENSE
- Continued bb-common parity

## [0.1.0] (2026-06-07)

### Added

- Initial operator: Package CRD and reconciler generating bb-common-equivalent
  resources (Istio PeerAuthentication/Sidecar, NetworkPolicies with egress/
  ingress shorthand and built-in definitions, AuthorizationPolicies, routes)
- Label-driven prune of stale resources on spec shrink
- Prometheus reconcile metrics and Warning events for Helm-only fields
- Basic CI; image and chart published to ghcr.io
