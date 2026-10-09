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
- **NetworkPolicies**: 7 baseline policies (deny-all and allow-in-ns in
  both directions, kube-DNS, istiod, prometheus-to-sidecar; an 8th,
  ambient-kubelet, under ambient mode) plus shorthand
  K8s/CIDR/definition/literal rules with HBONE port-15008 injection under
  ambient mode.
- **Routes**: `VirtualService` + `ServiceEntry` per inbound, gateway-permitting
  `NetworkPolicy`, TLS passthrough mode, advanced HTTP rules
  (match/rewrite/retries/fault), and outbound `ServiceEntry`.

**Documentation: [rjferguson21.github.io/bigbang-operator](https://rjferguson21.github.io/bigbang-operator/)**
(source under [`docs/`](docs/README.md)) — user guides for network policies,
routes, authorization policies, global configuration, controller behavior,
and bb-common migration. Design docs live in `plan/`, the in-flight roadmap
in `TODOS.md`.

## Install

The image is published to `ghcr.io/rjferguson21/bigbang-operator` and the
helm chart to `oci://ghcr.io/rjferguson21/charts/bigbang-operator`.

```sh
helm install bigbang-operator \
  oci://ghcr.io/rjferguson21/charts/bigbang-operator \
  --version 0.3.0 \
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
  --version 0.3.0 \
  --namespace bigbang-operator --create-namespace \
  --set image.repository=registry1.dso.mil/ironbank/big-bang/bigbang-operator
```

## Quickstart

Create a namespace and apply a minimal `Package`:

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
    ingress:
      to:
        my-app:8080:
          from:
            definition:
              gateway: true
EOF
```

This emits STRICT-mTLS `PeerAuthentication`, the baseline deny-all
NetworkPolicies with targeted allows (in-namespace, kube-DNS, istiod,
prometheus), and one generated policy admitting the Istio ingress gateway
to `my-app` pods on port 8080:

```sh
$ kubectl get packages -n my-app
NAME     READY   REASON             AGE
my-app   True    ResourcesApplied   5s

$ kubectl -n my-app get networkpolicy,peerauthentication
NAME      ...
networkpolicy.networking.k8s.io/allow-ingress-to-my-app-tcp-port-8080-from-gateway
networkpolicy.networking.k8s.io/default-egress-allow-all-in-ns
networkpolicy.networking.k8s.io/default-egress-allow-istiod
networkpolicy.networking.k8s.io/default-egress-allow-kube-dns
networkpolicy.networking.k8s.io/default-egress-deny-all
networkpolicy.networking.k8s.io/default-ingress-allow-all-in-ns
networkpolicy.networking.k8s.io/default-ingress-allow-prometheus-to-istio-sidecar
networkpolicy.networking.k8s.io/default-ingress-deny-all
peerauthentication.security.istio.io/default-peer-auth
```

Edit the spec and the resources follow; delete the `Package` and they're
garbage-collected. See [`docs/`](docs/README.md) for the full API —
egress/ingress shorthand, definitions, routes, and ambient mode.

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

Apache 2.0 — see [`LICENSE`](LICENSE).
