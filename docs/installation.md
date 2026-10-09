# Installation

The operator ships as a container image and a Helm chart, both published to
GHCR by the release pipeline:

- **Image**: `ghcr.io/rjferguson21/bigbang-operator`
- **Chart**: `oci://ghcr.io/rjferguson21/charts/bigbang-operator`

## Install with Helm

```sh
helm install bigbang-operator \
  oci://ghcr.io/rjferguson21/charts/bigbang-operator \
  --version 0.3.0 \
  --namespace bigbang-operator --create-namespace
```

The chart bundles the Package CRD (under `templates/crds/`), so a plain
`helm install` is the whole story — no separate CRD step.

Verify:

```sh
kubectl -n bigbang-operator get deploy
kubectl get crd packages.bigbang.dev
```

### On a Big Bang cluster

The Kyverno `restrict-image-registries` policy blocks ghcr.io. Apply the dev
PolicyException first:

```sh
kubectl apply -f hack/local-dev/policy-exception.yaml
```

For Iron Bank-hardened deploys, override the image repository instead:

```sh
helm install bigbang-operator \
  oci://ghcr.io/rjferguson21/charts/bigbang-operator \
  --version 0.3.0 \
  --namespace bigbang-operator --create-namespace \
  --set image.repository=registry1.dso.mil/ironbank/big-bang/bigbang-operator
```

## Helm chart reference

### What the chart renders

| Resource | Notes |
|---|---|
| Deployment | the controller manager; flags wired from values below |
| ServiceAccount | named after the release fullname (not configurable) |
| ClusterRole / ClusterRoleBinding | management of the six emitted kinds plus narrow extras — see [controller.md](controller.md#rbac) |
| Role / RoleBinding | leader-election leases in the release namespace |
| Package CRD | from `templates/crds/`, synced from `config/crd/bases` |
| ConfigMap (optional) | the [global config](global-config.md), only when `globalConfig.create: true` |

### Values

| Key | Default | Purpose |
|---|---|---|
| `image.repository` | `ghcr.io/rjferguson21/bigbang-operator` | override for Iron Bank or a mirror |
| `image.tag` | matches the chart version | pinned by the release pipeline |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | |
| `replicas` | `1` | >1 requires `leaderElection.enabled: true` |
| `resources` | 100m/128Mi requests, 500m/512Mi limits | |
| `leaderElection.enabled` | `true` | |
| `metrics.bindAddress` | `:8443` | `0` disables the metrics endpoint |
| `metrics.secure` | `true` | HTTPS + authn/authz on `/metrics` |
| `healthProbe.bindAddress` | `:8081` | liveness/readiness |
| `globalConfig.name` | `bigbang-operator-global` | the shared ConfigMap's name; namespace is always the release namespace |
| `globalConfig.create` | `false` | render the ConfigMap from this chart |
| `globalConfig.egressDefinitions` | `{}` | shared egress definitions (see [global-config.md](global-config.md)) |
| `globalConfig.ingressDefinitions` | `{}` | shared ingress definitions |
| `podSecurityContext` / `containerSecurityContext` | restricted (non-root, no privilege escalation, all caps dropped, read-only rootfs) | |
| `nodeSelector` / `tolerations` / `affinity` | empty | standard scheduling knobs |

### Shared definitions from values

With `globalConfig.create: true` the chart renders the global ConfigMap
directly from values, so central definitions live in your release values:

```yaml
globalConfig:
  create: true
  egressDefinitions:
    elasticsearch:
      ports:
        - port: 9200
          protocol: TCP
      to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              common.k8s.elastic.co/type: elasticsearch
```

## First Package

```sh
kubectl create namespace my-app
kubectl apply -f - <<'EOF'
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: my-app
  namespace: my-app
spec:
  istio:
    enabled: true
  networkPolicies:
    enabled: true
EOF
kubectl -n my-app get package my-app   # READY True / ResourcesApplied
```

See [network-policies.md](network-policies.md) and [routes.md](routes.md)
for what to put in the spec.

## Uninstall

```sh
helm uninstall bigbang-operator -n bigbang-operator
```

**The CRD is uninstalled with the chart.** It lives in `templates/crds/`
(a regular template, not Helm's install-once `crds/` root), so
`helm uninstall` deletes `packages.bigbang.dev` — which deletes every
Package CR, and owner-reference GC then removes all generated
NetworkPolicies, Istio resources, etc. If you want the policies to survive
an operator reinstall, back up your Package CRs first
(`kubectl get packages -A -o yaml`).
