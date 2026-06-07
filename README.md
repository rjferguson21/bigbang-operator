# bigbang-operator

A Kubernetes operator that reconciles a `Package` CR into the same set of
Istio + NetworkPolicy resources that Big Bang's
[`bb-common`](https://repo1.dso.mil/big-bang/product/packages/bb-common)
Helm chart emits — but as a controller, with status conditions, drift
recovery, and prune-on-spec-change instead of `helm upgrade`.

## What it emits

For a `Package` with all features on, the operator produces:

- **Istio**: `PeerAuthentication`, `Sidecar`, default `AuthorizationPolicy`
  resources, plus generated APs from NetworkPolicy shorthand and per-route
  APs that pin gateway-to-workload traffic to the gateway's ServiceAccount.
- **NetworkPolicies**: 7 baseline policies (deny-all, allow-in-ns,
  kube-DNS, istiod, prometheus-to-sidecar, ambient-kubelet, allow-all-in-ns)
  plus shorthand K8s/CIDR/definition rules with HBONE port-15008 injection
  under ambient mode.
- **Routes**: `VirtualService` + `ServiceEntry` per inbound, gateway-permitting
  `NetworkPolicy`, TLS passthrough mode, advanced HTTP rules
  (match/rewrite/retries/fault), and outbound `ServiceEntry`.

See `plan/` for the design docs and `TODOS.md` for the in-flight roadmap.

## Install

The image is published to `ghcr.io/rjferguson21/bigbang-operator` and the
helm chart to `oci://ghcr.io/rjferguson21/charts/bigbang-operator`.

```sh
helm install bigbang-operator \
  oci://ghcr.io/rjferguson21/charts/bigbang-operator \
  --version 0.1.0 \
  --namespace bigbang-operator --create-namespace
```

On a Big Bang cluster the Kyverno `restrict-image-registries` policy
blocks ghcr.io. Apply the dev PolicyException first:

```sh
kubectl apply -f hack/local-dev/policy-exception.yaml
```

For Iron Bank-hardened deploys, override the image repo:

```sh
helm install bigbang-operator \
  oci://ghcr.io/rjferguson21/charts/bigbang-operator \
  --version 0.1.0 \
  --namespace bigbang-operator --create-namespace \
  --set image.repository=registry1.dso.mil/ironbank/big-bang/bigbang-operator
```

## Try it

Apply a sample `Package`:

```sh
kubectl apply -f config/samples/bigbang_v1alpha1_package.yaml
kubectl get packages -A          # READY / REASON / AGE columns
kubectl -n example-app get peerauthentication,networkpolicy,virtualservice,serviceentry,authorizationpolicy
```

More samples under `config/samples/`. The `test/e2e/podinfo_smoke.sh`
script deploys the upstream podinfo chart and a `Package` shaped after
Big Bang's
[podinfo values](https://repo1.dso.mil/big-bang/product/maintained/podinfo/-/blob/main/chart/values.yaml)
end-to-end — `make podinfo-smoke` runs it.

## Develop

```sh
make dev-bbcluster   # bbtask default DISABLE_CORE=true (k3d + Big Bang)
make dev-deploy      # build, import to k3d, apply CRD + chart
make bb-smoke        # reconciler scenarios against the live cluster
make dev-undeploy
```

Inner loop (operator out-of-cluster, fastest):

```sh
make install         # CRDs only
make run             # manager against current kubeconfig
```

Tests: `make test` runs the generator goldens + the envtest reconciler suite.

## License

Apache 2.0 — see `LICENSE` headers.
