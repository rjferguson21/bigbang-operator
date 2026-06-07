#!/usr/bin/env bash
# bb_smoke.sh — end-to-end smoke test for the bigbang-operator.
#
# Pairs with `make dev-deploy`: assumes the operator is already running in
# the current kubectl context. Each test creates an isolated namespace,
# applies a Package CR, waits for Ready=True, asserts the expected resource
# set, mutates the spec, and asserts the pruned set.
#
# Exit code: 0 on success, non-zero on first failure.

set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-60s}"
KEEP_NS="${KEEP_NS:-}"        # set to non-empty to skip namespace deletion on success
FAILED=0

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
blue()  { printf '\033[34m%s\033[0m\n' "$*"; }

fail() {
    red "  FAIL: $*"
    FAILED=$((FAILED + 1))
}

run() {
    # Apply a Package manifest piped in via stdin, then wait for Ready.
    local ns="$1"
    "$KUBECTL" apply -f - >/dev/null
    "$KUBECTL" -n "$ns" wait package --all --for=condition=Ready --timeout="$WAIT_TIMEOUT" >/dev/null
}

assert_exists() {
    local ns="$1" kind="$2" name="$3"
    if ! "$KUBECTL" -n "$ns" get "$kind" "$name" >/dev/null 2>&1; then
        fail "expected $kind/$name in ns $ns, not found"
    fi
}

assert_absent() {
    local ns="$1" kind="$2" name="$3"
    if "$KUBECTL" -n "$ns" get "$kind" "$name" >/dev/null 2>&1; then
        fail "expected $kind/$name in ns $ns to be absent, but it exists"
    fi
}

assert_count() {
    local ns="$1" kind="$2" expected="$3"
    local got
    got=$("$KUBECTL" -n "$ns" get "$kind" --no-headers 2>/dev/null | wc -l | tr -d ' ')
    if [[ "$got" != "$expected" ]]; then
        fail "expected $expected $kind in ns $ns, got $got"
    fi
}

setup_ns() {
    local ns="$1"
    "$KUBECTL" delete ns "$ns" --ignore-not-found --wait=true >/dev/null 2>&1 || true
    "$KUBECTL" create ns "$ns" >/dev/null
}

cleanup_ns() {
    local ns="$1"
    [[ -n "$KEEP_NS" ]] && return 0
    "$KUBECTL" delete ns "$ns" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}

# ---------------------------------------------------------------------------
# Test 1: defaults-only — PeerAuth + 7 default NetworkPolicies.
# ---------------------------------------------------------------------------
test_defaults() {
    local ns="bb-e2e-defaults"
    blue "[test] defaults-only"
    setup_ns "$ns"

    cat <<EOF | run "$ns"
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata: { name: app, namespace: $ns }
spec:
  istio: { enabled: true, mtls: { mode: STRICT } }
  networkPolicies: { enabled: true }
EOF

    assert_exists "$ns" peerauthentication default-peer-auth
    assert_count  "$ns" networkpolicy 7
    cleanup_ns "$ns"
}

# ---------------------------------------------------------------------------
# Test 2: prune on spec shrink — disabling allow-istiod removes that netpol.
# ---------------------------------------------------------------------------
test_prune_on_shrink() {
    local ns="bb-e2e-prune"
    blue "[test] prune on spec shrink"
    setup_ns "$ns"

    cat <<EOF | run "$ns"
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata: { name: app, namespace: $ns }
spec:
  istio: { enabled: true }
  networkPolicies: { enabled: true }
EOF
    assert_exists "$ns" networkpolicy default-egress-allow-istiod

    cat <<EOF | run "$ns"
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata: { name: app, namespace: $ns }
spec:
  istio: { enabled: true }
  networkPolicies:
    enabled: true
    egress:
      defaults:
        allowIstiod: { enabled: false }
EOF
    assert_absent "$ns" networkpolicy default-egress-allow-istiod
    # Defaults still produce 6 of 7 default policies.
    assert_count  "$ns" networkpolicy 6
    cleanup_ns "$ns"
}

# ---------------------------------------------------------------------------
# Test 3: routes — VS + SE + gateway netpol + per-route AP.
# ---------------------------------------------------------------------------
test_routes_and_authz() {
    local ns="bb-e2e-routes"
    blue "[test] routes + per-route AuthorizationPolicy"
    setup_ns "$ns"

    cat <<EOF | run "$ns"
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata: { name: app, namespace: $ns }
spec:
  istio:
    enabled: true
    authorizationPolicies:
      enabled: true
      defaults: { enabled: false }
      generateFromNetpol: false
  networkPolicies: { enabled: true }
  routes:
    inbound:
      app:
        enabled: true
        gateways: [istio-gateway/public-ingressgateway]
        hosts: [app.bigbang.mil]
        service: app
        port: 8080
        selector: { app.kubernetes.io/name: app }
EOF

    assert_exists "$ns" virtualservice app
    assert_exists "$ns" serviceentry  app-internal
    assert_exists "$ns" authorizationpolicy app-public-ingressgateway-authz-policy
    # Routes also emit the gateway-permitting NetworkPolicy.
    assert_exists "$ns" networkpolicy allow-ingress-to-app-8080-from-ns-istio-gateway-pod-public-ingressgateway
    cleanup_ns "$ns"
}

# ---------------------------------------------------------------------------
# Test 4: CIDR shorthand with default excludeCIDRs (169.254.169.254/32).
# ---------------------------------------------------------------------------
test_cidr_with_exclude() {
    local ns="bb-e2e-cidr"
    blue "[test] CIDR shorthand + default excludeCIDRs"
    setup_ns "$ns"

    cat <<EOF | run "$ns"
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata: { name: app, namespace: $ns }
spec:
  networkPolicies:
    enabled: true
    egress:
      defaults: { enabled: false }
      from:
        app:
          to:
            cidr:
              0.0.0.0/0:443: true
EOF
    local np="allow-egress-from-app-to-anywhere-tcp-port-443"
    assert_exists "$ns" networkpolicy "$np"
    local except
    except=$("$KUBECTL" -n "$ns" get networkpolicy "$np" -o jsonpath='{.spec.egress[0].to[0].ipBlock.except[0]}')
    if [[ "$except" != "169.254.169.254/32" ]]; then
        fail "expected ipBlock.except[0]=169.254.169.254/32, got '$except'"
    fi
    cleanup_ns "$ns"
}

# ---------------------------------------------------------------------------
# Test 5: Sidecar emission then prune.
# ---------------------------------------------------------------------------
test_sidecar() {
    local ns="bb-e2e-sidecar"
    blue "[test] Sidecar emit + prune"
    setup_ns "$ns"

    cat <<EOF | run "$ns"
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata: { name: app, namespace: $ns }
spec:
  istio:
    enabled: true
    sidecar: { enabled: true, outboundTrafficPolicyMode: ALLOW_ANY }
EOF
    assert_exists "$ns" sidecar sidecar

    cat <<EOF | run "$ns"
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata: { name: app, namespace: $ns }
spec:
  istio: { enabled: true }
EOF
    assert_absent "$ns" sidecar sidecar
    cleanup_ns "$ns"
}

# ---------------------------------------------------------------------------
# entry point
# ---------------------------------------------------------------------------
main() {
    if ! "$KUBECTL" get deploy -n bigbang-operator 2>/dev/null | grep -q bigbang-operator; then
        red "no operator deployment found in ns 'bigbang-operator' — run 'make dev-deploy' first"
        exit 2
    fi

    test_defaults
    test_prune_on_shrink
    test_routes_and_authz
    test_cidr_with_exclude
    test_sidecar

    echo
    if [[ "$FAILED" -gt 0 ]]; then
        red "$FAILED failure(s)"
        exit 1
    fi
    green "all tests passed"
}

main "$@"
