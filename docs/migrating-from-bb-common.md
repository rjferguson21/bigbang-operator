# Migrating from bb-common

The Package spec is shaped after bb-common's values on purpose: for the
covered surface, a bb-common values block moves under `spec.` essentially
unchanged, and the emitted resources match bb-common's render — same names,
same specs, same labels/annotations (verified by side-by-side
`helm template` diffs; see `TODOS.md`'s parity audit trail).

bb-common values:

```yaml
networkPolicies:
  enabled: true
  ingress:
    to:
      my-app:8080:
        from:
          definition:
            gateway: true
```

The same thing as a Package:

```yaml
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: my-app
  namespace: my-app
spec:
  networkPolicies:
    enabled: true
    ingress:
      to:
        my-app:8080:
          from:
            definition:
              gateway: true
```

## What you gain

- Continuous reconciliation: drift (deleted/edited resources) is repaired,
  not discovered at the next `helm upgrade`.
- Prune on spec change, GC on Package delete (owner references).
- `Ready` status conditions with failure reasons, warning Events for
  unsupported fields, Prometheus metrics.
- Reconcile-time validation for routes (bb-common fails at render; the
  operator fails the Package's Ready condition with a precise message).

## Known divergences

Account for these differences when migrating:

| Area | bb-common | operator |
|---|---|---|
| `defaultsAsHooks` | emits Helm-hook copies of default policies | supported through the shared Helm helper; direct Packages warn |
| Helm templating in route `hosts`/`service` (`{{ .Values.domain }}`) | supported via `tpl` | shared Helm helper evaluates `tpl`; direct Packages need resolved strings |
| De-duplication | identical kind/name collisions renamed `-deduped-N` | not implemented; SSA makes a collision last-write-wins |
| Gateway netpol pod selector | `app.kubernetes.io/name` + `istio: ingressgateway` | `app.kubernetes.io/name` only (equivalent on real Big Bang gateways, where both labels are present) |
| Proto zero values | writes `action: ALLOW`, `location: MESH_EXTERNAL`, `resolution: NONE` explicitly | omits them (they are proto defaults); Istio behavior identical |
| `selfTest` | Helm template self-test harness | not applicable |
| `kubeAPI` definition ports | render-time `lookup` | reconcile-time lookup of the same Service; identical result, but stays current if the Service changes |

## Migration checklist

1. Move your `networkPolicies:` / `istio:` / `routes:` values under a
   Package's `spec:`.
2. For directly applied Packages, replace templated hostnames with literals
   (or a kustomize/GitOps substitution). The shared Helm helper evaluates them.
3. If you used `literal` rules or shorthand `metadata`, they work as-is.
4. For Helm-managed packages, use the [shared Helm helper](helm-hook-jobs.md).
   Keep `defaultsAsHooks` and per-rule hook metadata; the helper renders hook
   policies while the operator reconciles persistent policies.
5. Apply, then check `kubectl get package <name>` for `Ready=True` and
   diff the emitted resources against your previous Helm render if you want
   belt-and-suspenders.
