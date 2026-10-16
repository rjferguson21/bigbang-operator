# Development

Use **Helm 4.3.1**, the version pinned in the test and release workflows. Helm 4
is the supported baseline for local development and release publishing.
The `azure/setup-helm@v4` action version identifies the action, not the Helm CLI.
Keep its `version` inputs aligned in `helm.yml` and `image.yml`.

## Local development

The existing `make dev-bbcluster`, `make dev-deploy`, `make bb-smoke`, and
`make podinfo-smoke` commands work with Helm 4. `make dev-deploy` explicitly waits
for the operator rollout. The podinfo smoke test uses Helm's readiness wait and
then checks Package readiness separately. For a faster inner loop, use
`make install` and `make run` against a development cluster.

Run `make test` for generator and controller tests. Run `make test-helm` for live
chart validation; it requires Docker, k3d 5.8.3, kubectl, curl, and Helm 4.3.1. Allow enough
memory for a two-node k3d cluster, Istio CRDs, and a Docker image build.

## Chart validation

The Helm 4 workflow runs the same `make test-helm` target used locally. It:

- Lints the chart and renders the operator chart.
- Packages the chart, pushes it to a temporary OCI registry, pulls it back,
  and checks archive bytes and operator rendering.
- Installs the packaged operator, upgrades to two replicas, rolls back to one,
  and uninstalls it using `--wait=watcher`.
- Verifies persistent Package policies reconcile, survive operator upgrades and
  rollback, and are garbage-collected when the operator chart's CRD is uninstalled.

The harness creates its own kubeconfig and removes its cluster, registry, and
image on exit. It does not deploy to the current cluster. Test registry publishing
uses the same `<owner>/charts/<chart-name>:<version>` layout as release publishing;
HTTP is enabled only for that loopback registry. Production releases still use
GHCR authentication and TLS at:

- `oci://ghcr.io/rjferguson21/charts/bigbang-operator`

The test does not publish production versions or require registry credentials.
The release workflow packages the operator chart with the release version and publishes
only on version tags. See [installation](installation.md#moving-from-helm-3) for
CLI migration considerations. Hook networking is managed outside the operator.
