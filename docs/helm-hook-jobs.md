# Helm hook Jobs

Jobs such as kube-prometheus-stack's admission webhook certificate creation
and patch hooks need Kubernetes API access before the workload release is
ready. With default-deny egress, their pods need an allow policy that both
matches their labels and exists before they run. This applies in ambient
namespaces too; disabling sidecar injection does not grant network access.

Use one of the following patterns when migrating a chart to the operator.

## Prepare an operator-managed policy before the workload release

Manage a separate Package outside the workload Helm release. Install the
operator and its CRD first, then apply the Package and wait for it to become
Ready before installing the workload chart. Keep it in place for upgrades.

The [hook Jobs sample](https://github.com/rjferguson21/bigbang-operator/blob/main/config/samples/hook_jobs_package.yaml)
selects admission webhook pods for a release named `monitoring`:

```yaml
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: monitoring-hooks
  namespace: monitoring
spec:
  networkPolicies:
    enabled: true
    prependReleaseName: true
    ingress:
      defaults:
        enabled: false
    egress:
      defaults:
        enabled: false
      from:
        admission-hooks:
          podSelector:
            matchLabels:
              app.kubernetes.io/instance: monitoring
              app.kubernetes.io/component: prometheus-operator-webhook
          to:
            definition:
              kubeAPI: true
```

Match labels from the Jobs' **`spec.template.metadata.labels`**, not just
their top-level metadata. Adjust the namespace and instance label for your
release. `admission-hooks` names the rule; `podSelector` overrides the usual
`app.kubernetes.io/name` inference. A `"*"` source without a selector override
would allow every pod in the namespace, which is unnecessary here.

Disabling both default groups makes this Package emit only the hook allow
policy. The workload Package still owns its baseline policies. The name
prefix prevents resource-name collisions between the two Packages.

From a checkout, after creating the `monitoring` namespace:

```sh
kubectl apply -f config/samples/hook_jobs_package.yaml
kubectl -n monitoring wait --for=condition=Ready package/monitoring-hooks --timeout=120s
# Install the workload release only after the wait succeeds.
```

For GitOps, put this Package in an earlier deployment stage with a Package
Ready health check, and make the workload release depend on that stage.
Listing it first in the same manifest bundle does not wait for reconciliation.
After editing an existing Package, also verify that `status.observedGeneration`
matches `metadata.generation`; a previous Ready condition can briefly remain
while a new spec is waiting to reconcile.

The built-in [`kubeAPI` definition](network-policies.md#definitions) permits
the private IPv4 ranges and resolves API target ports from the Kubernetes
Service, falling back to unrestricted ports on lookup failure. Override it
with a [shared definition](global-config.md) or Package-local definition if
your API uses other addresses or you need narrower destinations. Add DNS
egress if the Job resolves hostnames, and declare any other required
destinations separately. NetworkPolicy allows do not replace the Job's RBAC.

Remove the bootstrap Package only after its hook Jobs are no longer needed;
deleting it garbage-collects its policy. Do not have the workload chart adopt
the same Package or generate a policy with the same name.

## When the Package must stay inside the workload release

Keep a chart-owned hook NetworkPolicy alongside the hook Jobs. A regular
Package in that release is created too late for pre-install hooks, and
reconciliation can also race post-install hooks. Adding `helm.sh/hook` to an
operator-generated policy cannot change Helm's execution order.

Add this template to the **workload chart**, with a weight lower than every
Job it supports in each selected hook phase:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ printf "%s-hook-api" (.Release.Name | trunc 54 | trimSuffix "-") }}
  namespace: {{ .Release.Namespace }}
  annotations:
    helm.sh/hook: pre-install,pre-upgrade,post-install,post-upgrade
    helm.sh/hook-weight: "-10"
    helm.sh/hook-delete-policy: before-hook-creation
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/instance: {{ .Release.Name | quote }}
      app.kubernetes.io/component: prometheus-operator-webhook
  policyTypes:
    - Egress
  egress:
    {{- required "hookJobNetworkPolicy.egress is required" .Values.hookJobNetworkPolicy.egress | toYaml | nindent 4 }}
```

Supply explicit API destinations in the workload chart's values. These are
illustrative addresses; replace them with your cluster's Service IP and all
API endpoints, including IPv6 destinations if applicable:

```yaml
hookJobNetworkPolicy:
  egress:
    - to:
        - ipBlock:
            cidr: 10.43.0.1/32
      ports:
        - protocol: TCP
          port: 443
    - to:
        - ipBlock:
            cidr: 192.0.2.10/32
      ports:
        - protocol: TCP
          port: 6443
```

These values belong to the example template, not the operator chart or
Package API. Inspect the actual destinations with:

```sh
kubectl -n default get service kubernetes -o yaml
kubectl -n default get endpointslices -l kubernetes.io/service-name=kubernetes -o yaml
```

Service address translation and NetworkPolicy enforcement order depend on
the networking implementation. Verify the effective destination IP and port
on your cluster; allowing only Service port 443 may miss target port 6443.
See Kubernetes' [NetworkPolicy guidance](https://kubernetes.io/docs/concepts/services-networking/network-policies/#networkpolicy-and-ipblock).

Use only `before-hook-creation` for this policy. `hook-succeeded` could remove
it as soon as Helm creates it, before the dependent Job runs. Helm does not
automatically remove hook resources on uninstall; delete the policy after
the release and any dependent Jobs are gone:

```sh
kubectl -n monitoring delete networkpolicy monitoring-hook-api --ignore-not-found
```

If the chart has rollback or delete hook Jobs needing API access, include
their phases too. Keep this policy's name distinct from operator-managed
policies and omit the operator's `bigbang.dev/package` label. Helm's
[hook lifecycle documentation](https://helm.sh/docs/topics/charts_hooks/)
describes weights, readiness, and deletion behavior.

Neither Package Ready nor Helm accepting a NetworkPolicy guarantees that
the network plugin has finished enforcing it. Hook clients should retry
transient connection failures; validate install and upgrade on the target
cluster.

## Diagnose a blocked hook

Inspect the Job pod's labels and logs, the matching egress policies, and the
API endpoints. For the example release:

```sh
kubectl -n monitoring get pods -l app.kubernetes.io/component=prometheus-operator-webhook --show-labels
kubectl -n monitoring get networkpolicies -o yaml
kubectl -n monitoring get package monitoring-hooks -o yaml
```

A connection refusal, timeout, or EOF alone does not establish that
NetworkPolicy is the cause. Check the CNI and, in ambient mode, ztunnel logs
alongside selector and destination matching. A successful connection with
HTTP 403 instead points to the Job's API authorization.

`networkPolicies.defaultsAsHooks` remains unsupported by the operator.
Preserve hook connectivity using one of the patterns above when removing
that bb-common setting.
