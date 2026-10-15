# Helm hook networking

Use the `bigbang-operator-package` Helm library to declare networking once for
both Helm hook Jobs and persistent operator-managed policies. The integration
uses bb-common's NetworkPolicy renderer, including its custom selectors,
definitions, defaults, and ambient HBONE handling. Helm 4 is the supported
baseline.

## Integrate a package chart

Install operator version `0.5.0` or later and its CRD before installing
consuming charts. Add this
dependency to the workload chart:

```yaml
dependencies:
  - name: bigbang-operator-package
    version: 0.5.0
    repository: oci://ghcr.io/rjferguson21/charts
```

Replace the chart's Package template with:

```helm
{{ include "bigbang-operator.package" . }}
```

The helper accepts `networkPolicies`, `istio`, and `routes` at the top level,
or under `bb-common`. Remove the old bb-common dependency/render entrypoints
when migrating: the integration bundles its renderer, and a second renderer
would create competing persistent resources. It evaluates templated values
and uses those declarations for both paths. Resource names default to including
the release name; preserve that setting when sharing a namespace.

Packages without enabled hook declarations render only their regular Package.

## Prometheus admission Jobs

For kube-prometheus-stack's certificate creation and webhook patch Jobs, add
this rule alongside the package's other networking declarations:

```yaml
networkPolicies:
  enabled: true
  defaultsAsHooks:
    enabled: true
  egress:
    from:
      admission-hooks:
        podSelector:
          matchLabels:
            app.kubernetes.io/instance: '{{ .Release.Name }}'
            app.kubernetes.io/component: prometheus-operator-webhook
        metadata:
          annotations:
            helm.sh/hook: pre-install,pre-upgrade,post-install,post-upgrade
            helm.sh/hook-weight: '-10'
        to:
          definition:
            kubeAPI: true
```

The selector matches both Job pod templates. The API allowance applies only
to those pods. `defaultsAsHooks` supplies enabled baseline policies such as
DNS; defaults alone do not grant API access. Other dependencies use the same
`k8s`, `cidr`, `definition`, or `literal` syntax as persistent policies.
NetworkPolicy access does not replace the Jobs' Kubernetes RBAC.

Install or upgrade normally:

```sh
helm upgrade --install monitoring ./chart -n monitoring --create-namespace
```

Helm creates the hook policies before the Jobs, even when another release
already enforces default-deny and the new regular Package does not exist yet.
The operator then reconciles persistent policies from the same declarations.
No wait Job, readiness endpoint, or per-package raw NetworkPolicy is needed.

## Ordering and retention

A hook policy's weight must be lower than the Jobs it supports in every
selected phase. Explicit weights are preserved; per-rule hooks without a
weight use `-10`. `defaultsAsHooks` retains bb-common's default weight `-5`
and phases `pre-install,pre-upgrade,post-delete`. Set its `hooks` and `weight`
when Jobs also run during rollback or deletion, or use earlier weights.

The shared helper emits a small cleanup Package hook after the application's
post hooks. This marker requests cleanup; it does not generate policies or
participate in bootstrapping. The operator removes matching temporary policies
once persistent policies are Ready. On uninstall it cleans without requiring
the regular Package to exist. Helm completing successfully does not wait for
this asynchronous cleanup.

```yaml
operatorHooks:
  cleanupWeight: 10000
  timeoutSeconds: 1800
```

`cleanupWeight` must exceed every application post-hook weight. The timeout
must exceed the entire expected operation, including image pulls and retries;
it is measured from each policy's creation. Choose a value between 1 and
86400 seconds. Failed or interrupted operations may never reach their cleanup
marker, so the operator also expires temporary policies at this deadline.
Cleanup resumes when the operator restarts after an outage.

Policy hook deletion is normalized to `before-hook-creation`, including when
values specify `hook-succeeded` or `hook-failed`. Helm 4 deletes successful
hooks at the end of a phase; this integration retains policies across phases
and until persistent reconciliation finishes. It does not rely on Helm's
non-Job readiness to prove that a network plugin has programmed its rules.
Hook clients should retry transient connection failures.

## Ownership and concurrent operations

Hook policies use release-scoped hashed names and the label
`hooks.bigbang.dev/release`. They have no Package owner reference or
`bigbang.dev/package` label, so normal pruning cannot delete them. Persistent
policies retain their normal Package ownership and do not carry Helm hook
annotations.

Cleanup checks namespace, release, render token, creation time, and deletion
preconditions. A stale marker cannot delete a newer policy or another
release's policies. Kubernetes creation timestamps have second precision;
policies created in the same second as a cleanup marker are conservatively
left to expiration. Retries replace existing hook policies using Helm's
`before-hook-creation` behavior. Rollback and delete hooks reuse stored
manifests, including their rendered dependency definitions.

Supported phases are pre/post install, upgrade, rollback, and delete.
`test` hooks are rejected explicitly. Include the phases that each Job needs;
a pre-install declaration does not imply rollback or uninstall access.

## Shared definitions and render-time lookup

Hook rendering reads the operator's global ConfigMap and merges definitions
with the same precedence as the operator: built-in, shared, then package-local.
The built-in `kubeAPI` definition uses bb-common's lookup of the Kubernetes
Service target ports. Cluster-specific overrides belong in the shared
ConfigMap, not duplicated in every chart.

If the operator uses a different ConfigMap location, configure it once in the
consuming chart's values:

```yaml
operatorHooks:
  globalConfig:
    namespace: bigbang-operator
    name: bigbang-operator-global
```

These are the defaults. An empty namespace disables shared lookup. Helm's
identity needs permission to read that ConfigMap and the Kubernetes Service.
Render with cluster access (`helm template --dry-run=server`) when checking
shared definitions. Offline rendering cannot discover cluster definitions or
API target ports; like bb-common, the built-in API definition then has no
port restriction. Hook definitions are snapshots from rendering; global
ConfigMap changes immediately update persistent policies, while hook snapshots
update on the next render (not a rollback of an old revision).

## Packages applied without Helm

The controller alone cannot schedule pre-install networking. A directly
applied Package with `defaultsAsHooks.enabled` still produces an
`UnsupportedField` warning directing the author to the shared helper.
Integrated Packages carry `hooks.bigbang.dev/integration: "true"` and do not
produce that warning.

For a temporary migration workaround, a separately managed Package can be
applied and awaited before installing the workload release. Keep it outside
that release's lifecycle. New integrations should use the shared helper.

See [Helm's hook lifecycle](https://helm.sh/docs/topics/charts_hooks/) and
[network-policy definitions](network-policies.md#definitions).
