# NetworkPolicies

Everything under `spec.networkPolicies`. Nothing is emitted unless
`networkPolicies.enabled: true`.

## Default policies

Eight baseline policies, all enabled by default and individually
suppressible. Names below assume `prependReleaseName: false` (the default);
with it on, every name gets a `<package-name>-` prefix.

| Name | Direction | Allows | Disable with | Notes |
|---|---|---|---|---|
| `default-egress-deny-all` | egress | nothing | `egress.defaults.denyAll.enabled: false` | the baseline; everything else punches holes in it |
| `default-egress-allow-all-in-ns` | egress | same-namespace pods | `egress.defaults.allowInNamespace.enabled: false` | |
| `default-egress-allow-kube-dns` | egress | UDP/TCP 53 → `kube-system` pods labeled `k8s-app: kube-dns` | `egress.defaults.allowKubeDns.enabled: false` | |
| `default-egress-allow-istiod` | egress | TCP 15012 → `istio-system`/istiod | `egress.defaults.allowIstiod.enabled: false` | suppressed under ambient (no sidecar → no istiod xDS) |
| `default-ingress-deny-all` | ingress | nothing | `ingress.defaults.denyAll.enabled: false` | |
| `default-ingress-allow-all-in-ns` | ingress | same-namespace pods | `ingress.defaults.allowInNamespace.enabled: false` | |
| `default-ingress-allow-prometheus-to-istio-sidecar` | ingress | TCP 15020 from `monitoring`/prometheus | `ingress.defaults.allowPrometheusToIstioSidecar.enabled: false` | suppressed under ambient |
| `default-ingress-allow-ambient-kubelet` | ingress | `169.254.7.127/32` (node-local kubelet probes) | — | only emitted when `istio.ambient.enabled: true` |

`egress.defaults.enabled: false` / `ingress.defaults.enabled: false` gate the
whole group per direction.

## Shorthand rules

Shorthand turns one line of YAML into a NetworkPolicy (and, when authz is
enabled, an AuthorizationPolicy — see
[authorization-policies.md](authorization-policies.md)).

### Egress: `egress.from.<pod>.to.*`

The outer key selects the **source** pod by `app.kubernetes.io/name=<pod>`
(`"*"` means every pod in the namespace). Four rule types nest under `to`:

```yaml
networkPolicies:
  enabled: true
  egress:
    from:
      my-app:                       # or "*"
        to:
          k8s:
            backend/api:8080: true            # ns/pod:port
            udp://monitoring/*:53: true       # protocol prefix, wildcard pod
            other-ns/worker:8000-9000: true   # port range
            third-ns/svc:[80,443]: true       # port list
          cidr:
            10.0.0.0/8:443: true              # CIDR with ports
          definition:
            kubeAPI: true                     # named definition (see below)
          literal:
            vault:                            # raw rule passthrough
              spec:
                - to:
                    - ipBlock:
                        cidr: 10.10.0.0/16
                  ports:
                    - port: 8200
                      protocol: TCP
```

Egress k8s key grammar: `[tcp|udp://]<ns>[/<pod>][:<ports>]` — ports as a
single port, `a-b` range, or `[a,b,c]` list. Protocol defaults to TCP.

### Ingress: `ingress.to.<pod[:ports]>.from.*`

Ports live on the **local** (destination) key; the remote key optionally
carries a ServiceAccount identity for AuthorizationPolicy generation:

```yaml
networkPolicies:
  enabled: true
  ingress:
    to:
      my-app:8080:                  # [tcp|udp://]<pod>[:<ports>]
        from:
          k8s:
            frontend/web: true                # ns/pod
            admin-sa@admin/dashboard: true    # identity@ns/pod → SPIFFE AP
          cidr:
            192.168.1.0/24: true              # bare CIDR (ports from local key)
          definition:
            gateway: true
          literal:
            loadbalancer:
              spec:
                - from:
                    - ipBlock:
                        cidr: 172.20.0.0/24
                  ports:
                    - port: 8080
                      protocol: TCP
```

### Generated names

Names mirror bb-common exactly, so dashboards and runbooks carry over:

- k8s egress: `allow-egress-from-<src>-to-ns-<ns>-pod-<pod>-tcp-port-8080`
- cidr egress: `allow-egress-from-<src>-to-cidr-10-0-0-0-8-tcp-port-443`
  (`0.0.0.0/0` renders as `-to-anywhere`)
- definition: `allow-egress-from-<src>-to-<defname>`
- literal: `allow-egress-from-<src>-to-<rulekey>` /
  `allow-ingress-to-<dst>-from-<rulekey>`
- no ports → `-any-port`; range → `-ports-8000-thru-9000`

### Rule values: `true` vs object

Every rule value accepts `true` (enable with defaults) or an object:

```yaml
k8s:
  backend/api:8080:
    enabled: true              # default true; false skips the rule
    podSelector:               # override remote pod selector
      app: custom
    namespaceSelector:         # override remote namespace selector
      team: a
    metadata:                  # extra labels/annotations (see below)
      labels:
        team: backend
```

Selectors accept both flat maps and `matchLabels:` nesting.

### Metadata overrides

A `metadata:` block (with `labels:` and/or `annotations:`) is accepted at the local (pod) level and
per-rule. Merge precedence, lowest to highest: local metadata → remote (rule)
metadata → operator-generated keys. Applied to the NetworkPolicy *and* any
AuthorizationPolicy generated from the same rule.

### Local pod selector override

```yaml
ingress:
  to:
    database:5432:
      podSelector:               # replaces app.kubernetes.io/name=database
        matchLabels:
          app: postgres
          tier: database
      from:
        k8s:
          backend/api: true
```

## Definitions

Reusable peer sets referenced via `to.definition.<name>` /
`from.definition.<name>`. Built-ins:

| Name | Direction | Peers |
|---|---|---|
| `kubeAPI` | egress | `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`; ports restricted to the API server's target ports, resolved at reconcile time from the `default/kubernetes` Service (all ports if the lookup fails) |
| `gateway` | ingress | namespace `istio-gateway`, pods labeled `istio: ingressgateway` |
| `monitoring` | ingress | namespace `monitoring`, pods labeled `app.kubernetes.io/name: prometheus` |

Custom definitions (same names override built-ins wholesale):

```yaml
networkPolicies:
  egress:
    definitions:
      external-api:
        to:
          - ipBlock:
              cidr: 203.0.113.0/24
        ports:
          - port: 443
            protocol: TCP
    from:
      my-app:
        to:
          definition:
            external-api: true
```

Referencing an unknown definition fails the reconcile
(`Ready=False / GenerationFailed`) — typos surface immediately.

Explicit empty selectors are preserved with their Kubernetes match-all
semantics: `namespaceSelector: {}` on a peer means "in any namespace" and
`podSelector: {}` means "all pods in the selected namespaces" — neither is
dropped from the generated policy.

## excludeCIDRs

```yaml
networkPolicies:
  egress:
    excludeCIDRs:
      - 169.254.169.254/32    # the default: cloud metadata endpoint
```

Each exclusion is added to an egress CIDR rule's `ipBlock.except` only when
strictly contained in the rule's CIDR. Applies to `cidr` shorthand only —
literal rules and `additionalPolicies` pass through untouched. Set an empty
list to disable.

## HBONE port injection (ambient)

When `istio.ambient.enabled: true` (or the explicit
`networkPolicies.hbonePortInjection.enabled: true`), a post-pass appends TCP
15008 to every rule that has explicit ports **and** at least one
namespaceSelector/podSelector peer — ambient's HBONE tunnel rides that port.
Rules with only ipBlock peers, or with no ports, are left alone. Mutated
policies are labeled
`ambient.istio.network-policies.bigbang.dev/hbone-injected: "true"`.

## Raw passthrough

```yaml
networkPolicies:
  additionalPolicies:        # `additional:` is accepted as a legacy alias
    - name: custom-policy
      labels:
        purpose: special
      spec:
        podSelector:
          matchLabels:
            role: special
        policyTypes:
          - Egress
        egress:
          - to:
              - ipBlock:
                  cidr: 192.168.0.0/16
```

The spec is emitted verbatim — no selector inference, no excludeCIDRs, no
HBONE skip (the HBONE pass sees these too).

## defaultsAsHooks

bb-common can emit Helm-hook copies of the default policies for install
ordering. Helm hooks have no operator equivalent — the operator applies and
repairs policies continuously — so these fields are accepted (the CRD types
are generated from bb-common's schema) but **ignored**, and a Package that
sets `defaultsAsHooks.enabled: true` gets a Warning event
(`UnsupportedField`) so the no-op is never silent.

## Labels on generated policies

Every generated NetworkPolicy carries:

```yaml
app.kubernetes.io/managed-by: bigbang-operator
bigbang.dev/package: <package-name>                      # the prune key
network-policies.bigbang.dev/source: bigbang-operator    # bb-common parity marker
network-policies.bigbang.dev/direction: egress|ingress
```

plus annotations recording provenance
(`generated.network-policies.bigbang.dev/local-key`, `remote-key`,
`from-definition`, `from-spec-literal`).
