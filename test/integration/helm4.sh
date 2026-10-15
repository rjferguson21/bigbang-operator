#!/usr/bin/env bash
# Validate Helm publishing and lifecycle behavior without using the current cluster.
set -euo pipefail
cd "$(dirname "$0")/../.."

case "$(helm version --short)" in
  v4.*) ;;
  *) echo 'Helm 4 is required' >&2; exit 1 ;;
esac

test_dir=$(mktemp -d)
cluster="operator-helm4-$$"
registry="${cluster}-registry"
image="bigbang-operator-helm4:$$"
registry_created=false
cluster_created=false
image_created=false
export KUBECONFIG="$test_dir/kubeconfig"
export HELM_CACHE_HOME="$test_dir/helm/cache"
export HELM_CONFIG_HOME="$test_dir/helm/config"
export HELM_DATA_HOME="$test_dir/helm/data"
cleanup() {
  result=$?
  trap - EXIT
  if [[ $result != 0 && -s "$KUBECONFIG" ]]; then
    kubectl get pods -A -o wide || true
    kubectl get events -A --sort-by=.lastTimestamp | tail -60 || true
    kubectl logs -n bigbang-operator deployment/operator-bigbang-operator --tail=100 || true
  fi
  if $cluster_created; then k3d cluster delete "$cluster" || true; fi
  if $registry_created; then docker rm -f "$registry" >/dev/null || true; fi
  if $image_created; then docker image rm "$image" >/dev/null || true; fi
  rm -rf "$test_dir"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

helm lint chart helm/package-integration
helm template operator chart > "$test_dir/operator.yaml"
mkdir -p "$test_dir/packages" "$test_dir/pulled"
helm package chart -d "$test_dir/packages"
version=$(helm show chart chart | sed -n 's/^version: //p')
helm package helm/package-integration --version "$version" -d "$test_dir/packages"

docker run -d --name "$registry" -p 127.0.0.1::5000 registry:2.8.3 >/dev/null
registry_created=true
address="127.0.0.1:$(docker port "$registry" 5000/tcp | cut -d: -f2)"
for attempt in {1..30}; do
  if curl -fsS "http://$address/v2/" >/dev/null; then break; fi
  sleep 1
done
curl -fsS "http://$address/v2/" >/dev/null
# Match the release workflow's <owner>/charts/<chart-name>:<version> layout.
for name in bigbang-operator bigbang-operator-package; do
  helm push "$test_dir/packages/$name-$version.tgz" "oci://$address/rjferguson21/charts" --plain-http
  helm pull "oci://$address/rjferguson21/charts/$name" --version "$version" --plain-http --destination "$test_dir/pulled"
  cmp "$test_dir/packages/$name-$version.tgz" "$test_dir/pulled/$name-$version.tgz"
done
helm template operator "$test_dir/pulled/bigbang-operator-$version.tgz" > "$test_dir/pulled.yaml"
cmp "$test_dir/operator.yaml" "$test_dir/pulled.yaml"

# k3s supplies NetworkPolicy enforcement for the hook isolation assertions.
cluster_created=true
k3d cluster create "$cluster" --image rancher/k3s:v1.35.6-k3s1 --agents 1 \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false \
  --k3s-arg '--disable=traefik,servicelb@server:*' --wait
k3d kubeconfig get "$cluster" > "$KUBECONFIG"

helm repo add istio https://istio-release.storage.googleapis.com/charts
helm install istio-base istio/base --version 1.30.4 -n istio-system --create-namespace --wait=watcher
helm install istiod istio/istiod --version 1.30.4 -n istio-system --set profile=ambient --wait=watcher
helm install istio-cni istio/cni --version 1.30.4 -n istio-system \
  --set profile=ambient --set cniBinDir=/var/lib/rancher/k3s/data/cni \
  --set cniConfDir=/var/lib/rancher/k3s/agent/etc/cni/net.d --wait=watcher
helm install ztunnel istio/ztunnel --version 1.30.4 -n istio-system --wait=watcher

docker build -t "$image" .
image_created=true
k3d image import "$image" -c "$cluster"
operator_chart="$test_dir/pulled/bigbang-operator-$version.tgz"
operator_flags=(-n bigbang-operator --set "image.repository=${image%:*}" --set "image.tag=${image##*:}" --wait=watcher --timeout=180s)
helm install operator "$operator_chart" --create-namespace "${operator_flags[@]}"
helm upgrade operator "$operator_chart" --set replicas=2 "${operator_flags[@]}"
[[ $(kubectl -n bigbang-operator get deployment operator-bigbang-operator -o jsonpath='{.spec.replicas}') == 2 ]]
helm rollback operator 1 -n bigbang-operator --wait=watcher --timeout=180s
[[ $(kubectl -n bigbang-operator get deployment operator-bigbang-operator -o jsonpath='{.spec.replicas}') == 1 ]]

python3 test/integration/helm_hooks.py --kubeconfig "$KUBECONFIG" \
  --library-chart "$test_dir/pulled/bigbang-operator-package-$version.tgz"
helm uninstall operator -n bigbang-operator --wait=watcher --timeout=180s
[[ -z $(kubectl get crd packages.bigbang.dev --ignore-not-found -o name) ]]
[[ -z $(kubectl -n bigbang-operator get deployment operator-bigbang-operator --ignore-not-found -o name) ]]
echo 'PASS: Helm 4 chart rendering, OCI round-trip, operator lifecycle, and hook lifecycle'
