# `spec.networkPolicies`

bb-common keys under `networkPolicies` generate Kubernetes `NetworkPolicy`
resources. Three declaration styles coexist: **defaults**, **shorthand**, and
**raw**.

Source: `bb-common/chart/templates/network-policies/`, schema at
`bb-common/chart/values.schema.json:284`.

## Top-level fields

| Field | Type | Default | Effect |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Master switch. Nothing under `networkPolicies` is emitted unless `true`. |
| `prependReleaseName` | bool | `false` | Prepend release name to every emitted `NetworkPolicy`. |
| `hbonePortInjection.enabled` | bool | `false` | Inject HBONE port 15008 into rules with port specs (auto-on under ambient). |
| `defaultsAsHooks.enabled` | bool | `false` | Emit a `-as-hook` copy of each default policy as a Helm hook. |
| `defaultsAsHooks.hooks[]` | list | `[pre-install, pre-upgrade, post-delete]` | Hook phases. |
| `defaultsAsHooks.weight` | int | `-5` | Hook weight. |
| `defaultsAsHooks.deletePolicies[]` | list | `[hook-succeeded, before-hook-creation]` | Hook delete policies. |
| `egress.excludeCIDRs[]` | list | `[169.254.169.254/32]` | CIDRs stripped from all ipBlock egress rules. |
| `additionalPolicies[]` | list | `[]` | Raw `NetworkPolicy` passthrough (preferred). |
| `additional[]` | list | `[]` | Legacy alias for `additionalPolicies`. |

## Defaults — `egress.defaults.*` / `ingress.defaults.*`

Auto-emitted when `networkPolicies.enabled: true`. Each is individually
toggleable; `defaults.enabled: false` disables a whole side at once.

| Side | Policy | Name | Notes |
| --- | --- | --- | --- |
| egress | `denyAll` | `default-egress-deny-all` | Baseline deny. |
| egress | `allowInNamespace` | `default-egress-allow-all-in-ns` | Allow same namespace. |
| egress | `allowKubeDns` | `default-egress-allow-kube-dns` | UDP/TCP 53 to `kube-system/kube-dns`. |
| egress | `allowIstiod` | `default-egress-allow-istiod` | TCP 15012 to `istio-system/istiod`. **Skipped when `istio.ambient.enabled: true`.** |
| ingress | `denyAll` | `default-ingress-deny-all` | Baseline deny. |
| ingress | `allowInNamespace` | `default-ingress-allow-all-in-ns` | Allow same namespace. |
| ingress | `allowPrometheusToIstioSidecar` | `default-ingress-allow-prometheus-to-istio-sidecar` | TCP 15020 from `monitoring/prometheus`. |
| ingress | `allowAmbientKubelet` | `default-ingress-allow-ambient-kubelet` | For Istio ambient mode kubelet probes. |

All defaults carry:

```yaml
metadata:
  labels:
    network-policies.bigbang.dev/source: bb-common
    network-policies.bigbang.dev/direction: egress  # or ingress
```

## Shorthand — `egress.from.*` / `ingress.to.*`

Compact key-only form for the common "namespace + workload + port" rule.

```yaml
networkPolicies:
  egress:
    from:
      <src-pod-name>:           # matches app.kubernetes.io/name=<src>; use "*" for any
        to:
          k8s:
            <ns>/<pod>: true
            <ns>/<pod>:<port>: true
            <ns>/<pod>:<port1>-<port2>: true      # port range
            <ns>/<pod>:[<p1>,<p2>]: true          # port list
            udp://<ns>/<pod>:<port>: true         # protocol prefix
  ingress:
    to:
      <dst-pod-name>:           # matches app.kubernetes.io/name=<dst>
        from:
          k8s:
            <ns>/<pod>: true
```

Generated names follow:

* egress → `allow-egress-from-<src>-to-ns-<ns>-pod-<pod>-<proto>-port[s]-<port>`
* ingress → `allow-ingress-to-<dst>-<proto>-port[s]-<port>-from-ns-<ns>-pod-<pod>`

(`<src>` becomes `any-pod` when `*`; `*-port[s]-*` becomes `any-port` when no
port given.)

Duplicate specs are de-duplicated; collisions with differing specs get
`-deduped-N` suffixes (see `tests/network-policies/ingress_test.yaml`).

## Raw — `additionalPolicies[]`

```yaml
networkPolicies:
  additionalPolicies:
    - name: my-policy
      labels:    { ... }      # optional, merged with default labels
      annotations: { ... }    # optional
      spec:                   # raw NetworkPolicy spec, Helm templating allowed
        podSelector: { ... }
        policyTypes: [Egress]
        egress:
          - to:    [ ... ]
            ports: [ ... ]
```

## Resources emitted

| Source | Kind | Naming |
| --- | --- | --- |
| each enabled default | `NetworkPolicy` | `default-{egress,ingress}-<policy-name>` (+ `-as-hook` when `defaultsAsHooks.enabled`) |
| each shorthand egress entry | `NetworkPolicy` | `allow-egress-from-<src>-to-ns-<ns>-pod-<pod>-<proto>-port[s]-<port>` |
| each shorthand ingress entry | `NetworkPolicy` | `allow-ingress-to-<dst>-<proto>-port[s]-<port>-from-ns-<ns>-pod-<pod>` |
| each `additionalPolicies[]` | `NetworkPolicy` | name from entry |

## Operator notes

* The shorthand parser does spec de-duplication. The operator should not
  attempt its own dedup — let bb-common produce the canonical set, then
  apply.
* `prependReleaseName: true` produces stable names tied to `metadata.name`
  of the `Package`. The operator's prune logic should compare the *current*
  rendered names rather than caching the prior set verbatim.
