#!/usr/bin/env bash
# podinfo_smoke.sh — deploy the upstream podinfo helm chart and a Package CR
# shaped after Big Bang's podinfo values
# (https://repo1.dso.mil/big-bang/product/maintained/podinfo, chart/values.yaml).
#
# Note: the BB chart pulls in the upstream `podinfo` chart aliased to
# `upstream`, so its resource names look like `podinfo-upstream`. We deploy
# upstream directly (release name `podinfo`) and adjust the Package CR
# accordingly — same shape, no aliasing.
#
# Kyverno will block the upstream ghcr.io image + floating tag; we apply a
# scoped PolicyException for the test namespace.
#
# Pairs with `make dev-deploy` — assumes the operator is already running.

set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
HELM="${HELM:-helm}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-180s}"
NS="${NS:-bb-e2e-podinfo}"
RELEASE="${RELEASE:-podinfo}"
KEEP_NS="${KEEP_NS:-}"
FAILED=0

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
blue()  { printf '\033[34m%s\033[0m\n' "$*"; }

fail() {
    red "  FAIL: $*"
    FAILED=$((FAILED + 1))
}

apply_pkg() {
    "$KUBECTL" apply -f - >/dev/null
    "$KUBECTL" -n "$NS" wait package --all --for=condition=Ready --timeout="$WAIT_TIMEOUT" >/dev/null
}

assert_exists() {
    local kind="$1" name="$2"
    if ! "$KUBECTL" -n "$NS" get "$kind" "$name" >/dev/null 2>&1; then
        fail "expected $kind/$name, not found"
    fi
}

assert_count() {
    local kind="$1" expected="$2"
    local got
    got=$("$KUBECTL" -n "$NS" get "$kind" --no-headers 2>/dev/null | wc -l | tr -d ' ')
    if [[ "$got" != "$expected" ]]; then
        fail "expected $expected $kind, got $got"
        "$KUBECTL" -n "$NS" get "$kind" --no-headers 2>&1 | sed 's/^/    /'
    fi
}

# ---------------------------------------------------------------------------
# Pre: Kyverno PolicyException for the upstream image + floating tag.
# The cluster runs with --exceptionNamespace=kyverno, so the PE must live
# in the kyverno namespace regardless of which namespaces it scopes.
# ---------------------------------------------------------------------------
apply_policy_exception() {
    blue "[pre] PolicyException for $NS in kyverno namespace"
    cat <<EOF | "$KUBECTL" apply -f - >/dev/null
apiVersion: kyverno.io/v2
kind: PolicyException
metadata:
  name: podinfo-e2e-$NS
  namespace: kyverno
spec:
  exceptions:
    - policyName: restrict-image-registries
      ruleNames: [validate-registries, autogen-validate-registries]
    - policyName: disallow-image-tags
      ruleNames: [validate-image-tag, autogen-validate-image-tag]
  match:
    any:
      - resources:
          kinds: [Pod, Deployment, ReplicaSet]
          namespaces: [$NS]
EOF
}

cleanup_policy_exception() {
    "$KUBECTL" -n kyverno delete policyexception "podinfo-e2e-$NS" --ignore-not-found >/dev/null 2>&1 || true
}

# ---------------------------------------------------------------------------
# Step 1: deploy the upstream podinfo helm chart with redis enabled.
# ---------------------------------------------------------------------------
deploy_workload() {
    blue "[step] helm install upstream podinfo (with redis)"
    "$HELM" upgrade --install "$RELEASE" podinfo \
        --repo https://stefanprodan.github.io/podinfo \
        --namespace "$NS" \
        --version 6.13.0 \
        --set redis.enabled=true \
        --set cache='tcp://podinfo-redis:6379' \
        --wait --timeout "$WAIT_TIMEOUT" >/dev/null

    # Confirm pods up and the two services we'll reference exist.
    "$KUBECTL" -n "$NS" rollout status deploy/"$RELEASE" --timeout="$WAIT_TIMEOUT" >/dev/null
    assert_exists service "$RELEASE"
    assert_exists service "$RELEASE-redis"
}

# ---------------------------------------------------------------------------
# Step 2: apply the Package CR (shape lifted from BB podinfo values,
# names adjusted to match the un-aliased release).
# ---------------------------------------------------------------------------
apply_package() {
    blue "[step] apply Package CR + assert resources"

    cat <<EOF | apply_pkg
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: $RELEASE
  namespace: $NS
spec:
  istio:
    enabled: true
    authorizationPolicies:
      enabled: true
      generateFromNetpol: true
  networkPolicies:
    enabled: true
    ingress:
      to:
        $RELEASE:9898:
          from:
            k8s:
              monitoring-monitoring-kube-prometheus@monitoring/prometheus:
                enabled: true
        $RELEASE-redis:6379:
          podSelector:
            matchLabels:
              app.kubernetes.io/name: redis
          from:
            k8s:
              default@$NS/$RELEASE: true
  routes:
    inbound:
      $RELEASE:
        enabled: true
        hosts:
          - $RELEASE.dev.bigbang.mil
        gateways:
          - istio-gateway/public-ingressgateway
        service: $RELEASE
        port: 9898
        selector:
          app.kubernetes.io/name: $RELEASE
EOF

    assert_exists peerauthentication default-peer-auth
    # 7 defaults + 2 shorthand-ingress + 1 route gateway-permitting.
    assert_count  networkpolicy 10
    assert_exists networkpolicy "allow-ingress-to-$RELEASE-tcp-port-9898-from-ns-monitoring-pod-prometheus"
    assert_exists networkpolicy "allow-ingress-to-$RELEASE-redis-tcp-port-6379-from-ns-$NS-pod-$RELEASE"
    assert_exists networkpolicy "allow-ingress-to-$RELEASE-9898-from-ns-istio-gateway-pod-public-ingressgateway"

    # 2 defaults + 2 from-netpol + 1 per-route. AP names use the
    # `<sa>@<ns>/<pod>` shorthand's identity (the part before `@`).
    assert_count  authorizationpolicy 5
    assert_exists authorizationpolicy "$RELEASE-public-ingressgateway-authz-policy"
    assert_exists authorizationpolicy "allow-ingress-to-$RELEASE-tcp-port-9898-from-ns-monitoring-with-identity-monitoring-monitoring-kube-prometheus"
    assert_exists authorizationpolicy "allow-ingress-to-$RELEASE-redis-tcp-port-6379-from-ns-$NS-with-identity-default"

    assert_exists virtualservice "$RELEASE"
    assert_exists serviceentry   "$RELEASE-internal"
}

# ---------------------------------------------------------------------------
# Step 3: smoke the actual HTTP path — curl podinfo through its in-cluster
# Service. (Going through the gateway would need an external IP / port-fwd;
# in-cluster confirms the workload itself is healthy and the NetworkPolicy
# defaults don't block the test pod from reaching it.)
# ---------------------------------------------------------------------------
smoke_http() {
    # End-to-end traffic check: hit podinfo through the public-ingressgateway
    # using the hostname the VirtualService binds. Requires /etc/hosts (set
    # up by `bbtask default`) to resolve $RELEASE.dev.bigbang.mil to the
    # gateway's external IP.
    local host="$RELEASE.dev.bigbang.mil"
    blue "[step] curl https://$host/healthz through the gateway"

    local status
    status=$(curl -ks --max-time 10 -o /dev/null -w '%{http_code}' "https://$host/healthz" || echo "000")
    if [[ "$status" != "200" ]]; then
        fail "expected HTTP 200 from $host/healthz, got $status"
    fi
}

# ---------------------------------------------------------------------------
# Step 4: drift recovery — delete an emitted child, confirm operator
# recreates it.
# ---------------------------------------------------------------------------
test_drift_recovery() {
    blue "[step] drift recovery — delete a child, expect recreate"
    local target="allow-ingress-to-$RELEASE-redis-tcp-port-6379-from-ns-$NS-pod-$RELEASE"
    "$KUBECTL" -n "$NS" delete networkpolicy "$target" --wait=true >/dev/null

    local deadline=$((SECONDS + 15))
    while (( SECONDS < deadline )); do
        "$KUBECTL" -n "$NS" get networkpolicy "$target" >/dev/null 2>&1 && return 0
        sleep 1
    done
    fail "deleted $target was not recreated within 15s"
}

# ---------------------------------------------------------------------------
# entry point
# ---------------------------------------------------------------------------
main() {
    if ! "$KUBECTL" get deploy -n bigbang-operator 2>/dev/null | grep -q bigbang-operator; then
        red "no operator deployment found in ns 'bigbang-operator' — run 'make dev-deploy' first"
        exit 2
    fi

    "$KUBECTL" delete ns "$NS" --ignore-not-found --wait=true >/dev/null 2>&1 || true
    "$KUBECTL" create ns "$NS" >/dev/null

    apply_policy_exception
    deploy_workload
    apply_package
    smoke_http
    test_drift_recovery

    if [[ -z "$KEEP_NS" ]]; then
        "$HELM" uninstall "$RELEASE" -n "$NS" --wait >/dev/null 2>&1 || true
        cleanup_policy_exception
        "$KUBECTL" delete ns "$NS" --ignore-not-found --wait=false >/dev/null 2>&1 || true
    fi

    echo
    if [[ "$FAILED" -gt 0 ]]; then
        red "$FAILED failure(s)"
        exit 1
    fi
    green "all tests passed"
}

main "$@"
