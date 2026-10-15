# Helm hook lifecycle test

Use a disposable cluster with NetworkPolicy enforcement, Istio ambient, and the
current operator running. The test creates namespaces and scoped test RBAC,
runs the real kube-webhook-certgen create/patch commands, and removes its releases
and workload namespaces. A backend fixture remains in `hook-dependencies` for
inspection. The existing cluster context is not changed.

```sh
python3 test/integration/helm_hooks.py --kubeconfig /path/to/isolated.kubeconfig
```

Requires Helm 4, kubectl, and Python 3. It tests fresh installs under an existing
release's deny-all policies, custom selectors, API and service dependencies,
upgrade, rollback, uninstall, failure expiration, retry, and unrelated-pod
denial, with and without ambient redirection.
