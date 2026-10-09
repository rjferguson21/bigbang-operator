# Global configuration

Cluster-wide operator configuration shared by every Package, delivered
through a single ConfigMap. Today it carries shared egress/ingress
definitions; it is the designated home for any future "define once, use
everywhere" settings.

## The ConfigMap

By default the operator reads `bigbang-operator-global` in its own
namespace. Two data keys are recognized, each a YAML map using the exact
same schema as the package-local
`networkPolicies.{egress,ingress}.definitions` blocks:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: bigbang-operator-global
  namespace: bigbang-operator
data:
  egressDefinitions: |
    elasticsearch:
      ports:
        - port: 9200
          protocol: TCP
      to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              common.k8s.elastic.co/type: elasticsearch
  ingressDefinitions: |
    argocd:
      from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: argocd
          podSelector:
            matchLabels:
              app.kubernetes.io/name: argocd-server
```

Any Package can then reference these like any other definition:

```yaml
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: my-app
  namespace: my-app
spec:
  networkPolicies:
    enabled: true
    egress:
      from:
        my-app:
          to:
            definition:
              elasticsearch: true
    ingress:
      to:
        my-app:8080:
          from:
            definition:
              argocd: true
```

## Precedence

On a name collision the most specific layer wins:

```
built-in  <  shared (global ConfigMap)  <  package-local
```

- A shared definition can override a built-in (`kubeAPI`, `gateway`,
  `monitoring`) cluster-wide.
- A package-local definition always shadows a shared one of the same name —
  packages can opt out of central definitions without coordination.

## Change propagation

The controller watches the ConfigMap (the informer is field-selected to that
single object — no cluster-wide ConfigMap caching) and re-reconciles every
Package on any edit. Changing a shared definition updates the generated
NetworkPolicies everywhere it is referenced, without touching Package specs.

## Location and discovery

| Setting | Flag | Chart value | Default |
|---|---|---|---|
| Name | `--global-config-name` | `globalConfig.name` | `bigbang-operator-global` |
| Namespace | `--global-config-namespace` | (release namespace) | `$POD_NAMESPACE` |

An empty namespace disables the feature entirely — no fetch, no watch. This
is the natural state for `make run` outside a pod without `POD_NAMESPACE`
set.

## Managing the ConfigMap

Two options:

- **From the chart** — set `globalConfig.create: true` and author the
  definitions under `globalConfig.egressDefinitions` /
  `globalConfig.ingressDefinitions` in values; the chart renders the
  ConfigMap into the release namespace.
- **Out-of-band** — manage the ConfigMap from a GitOps repo or another
  chart; the operator only needs it to exist under the configured
  name/namespace.

## Failure modes

| State | Behavior |
|---|---|
| ConfigMap absent | No-op: only built-in and package-local definitions resolve |
| ConfigMap present, valid | Shared pool participates in resolution |
| ConfigMap present, malformed | Any Package resolving a definition that is **not** package-local fails its reconcile (`Ready=False`, reason `GenerationFailed`, message `global config unreadable: ...`) — the shared pool could have held or overridden the name, so the operator refuses to guess. Package-local definitions keep resolving. |

Parsing is strict: unknown fields in a definition are errors, matching the
CRD's pruning of package-local definitions.
