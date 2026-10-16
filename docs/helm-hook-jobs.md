# Helm hook networking

The operator manages persistent resources declared by a Package. It does not
create temporary hook policies, coordinate Helm hook Jobs, or perform hook cleanup.
The `bigbang-operator-package` helper chart is removed as of version 0.6.0 and is
no longer published.

## Providing networking for hook Jobs

Pre-install hooks run before Helm creates ordinary resources, including a Package
in the same release. Therefore, that Package cannot supply networking needed by
pre-install Jobs. Hook annotations on a Package do not make Helm wait for the
operator's Ready condition.

Provide the policies in the application chart's hooks, or apply a separately
managed networking Package and await its current generation's Ready condition
before installing the application. Keep separately managed networking available
through upgrade, rollback, and uninstall hooks, and remove it only after dependent
Jobs finish. The deployment workflow owns that ordering and cleanup.

For example, Prometheus admission Jobs need policies selecting their pods and
allowing API access, plus any required DNS and mesh connectivity. Their Kubernetes
RBAC remains separate from network access.

`networkPolicies.defaultsAsHooks.enabled` is accepted for values compatibility
but produces an `UnsupportedField` warning. Default policies still reconcile
continuously; no hook copies are created.

## Migrating from the retired helper

Before upgrading to 0.6.0:

1. Replace `{{ include "bigbang-operator.package" . }}` with an ordinary Package
   template using the same resolved `networkPolicies`, `istio`, and `routes` spec.
   Preserve the Package's name, namespace, and resource naming settings.
2. Provide application hook networking through your chart or deployment workflow,
   then remove the helper dependency and `operatorHooks` values. Remove the old
   `hooks.bigbang.dev/*` integration annotations from the Package template.
3. Finish any in-flight Helm operations and remove their old temporary policies
   and cleanup markers before upgrading the operator. Review resources labeled
   `hooks.bigbang.dev/release` and Package markers annotated
   `hooks.bigbang.dev/action: cleanup`, scoped to the relevant release and namespace.

The new operator does not expire leftover temporary policies or process old
cleanup markers. Existing application releases using the helper must migrate;
removing the helper from this repository does not rewrite their stored manifests.
