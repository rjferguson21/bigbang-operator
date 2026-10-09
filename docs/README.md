# bigbang-operator docs

The operator reconciles a `Package` custom resource into the same Istio +
NetworkPolicy resources that Big Bang's
[bb-common](https://repo1.dso.mil/big-bang/product/packages/bb-common) Helm
chart renders — but as a controller: continuously applied, drift-recovered,
pruned on spec change, with status conditions instead of `helm status`.

A `Package` spec is intentionally shaped like bb-common's values. If you know
how to write `networkPolicies:` / `istio:` / `routes:` blocks for bb-common,
you already know the Package API.

## Guides

| Doc | Covers |
|---|---|
| [network-policies.md](network-policies.md) | Default policies, egress/ingress shorthand (k8s, cidr, definition, literal), metadata overrides, `excludeCIDRs`, HBONE injection, raw passthrough |
| [istio.md](istio.md) | PeerAuthentication, Sidecar, ambient mode side-effects, custom ServiceEntries |
| [authorization-policies.md](authorization-policies.md) | Default APs, APs generated from NetworkPolicy shorthand, per-route gateway APs, custom APs |
| [routes.md](routes.md) | Inbound routes (VirtualService, ServiceEntry, gateway netpol, gateway AP, TLS passthrough, advanced HTTP), outbound ServiceEntries |
| [controller.md](controller.md) | Reconcile flow, server-side apply, pruning, drift recovery, status conditions, events, metrics, RBAC |
| [migrating-from-bb-common.md](migrating-from-bb-common.md) | Parity status and the (few, deliberate) divergences |

## Quick start

```yaml
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: example-app
  namespace: example-app
spec:
  istio:
    enabled: true
  networkPolicies:
    enabled: true
```

This emits 1 `PeerAuthentication` (STRICT mTLS) and 7 baseline
NetworkPolicies (deny-all + targeted allows). Watch it land:

```sh
kubectl apply -f config/samples/bigbang_v1alpha1_package.yaml
kubectl get packages -A                        # READY / REASON / AGE
kubectl -n example-app get netpol,peerauthentication
```

Real-world examples live in [`config/samples/`](https://github.com/rjferguson21/bigbang-operator/tree/main/config/samples) —
including `Package`s shaped after Big Bang's kiali and loki values.

Design history (why decisions were made) is in [`plan/`](https://github.com/rjferguson21/bigbang-operator/tree/main/plan); these
docs describe what the operator does today.
