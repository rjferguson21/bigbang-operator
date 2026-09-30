# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Explicit empty `namespaceSelector: {}` / `podSelector: {}` in network policy
  definitions were dropped, silently narrowing any-namespace rules to
  same-namespace-only (#1)

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
