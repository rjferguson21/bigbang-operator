{{- define "bigbang-operator.package" -}}
{{- $settings := .Values -}}
{{- if hasKey .Values "bb-common" -}}
  {{- $settings = index .Values "bb-common" -}}
{{- end -}}
{{- $spec := dict -}}
{{- range $key := list "networkPolicies" "istio" "routes" -}}
  {{- if hasKey $settings $key -}}
    {{- $value := deepCopy (index $settings $key) -}}
    {{- if not (hasKey $value "prependReleaseName") -}}
      {{- $_ := set $value "prependReleaseName" true -}}
    {{- end -}}
    {{- $_ := set $spec $key $value -}}
  {{- end -}}
{{- end -}}
{{- $spec = tpl (toYaml $spec) . | fromYaml -}}
{{- $token := randAlphaNum 24 -}}
{{- $hooks := default dict .Values.operatorHooks -}}
{{- $timeout := dig "timeoutSeconds" 1800 $hooks -}}
{{- if or (not (regexMatch "^[0-9]+$" (toString $timeout))) (lt (int $timeout) 1) (gt (int $timeout) 86400) -}}
  {{- fail "operatorHooks.timeoutSeconds must be an integer between 1 and 86400" -}}
{{- end -}}
{{- $cleanupWeight := dig "cleanupWeight" 10000 $hooks -}}
{{- if not (regexMatch "^-?[0-9]+$" (toString $cleanupWeight)) -}}
  {{- fail "operatorHooks.cleanupWeight must be an integer" -}}
{{- end -}}
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: {{ .Release.Name }}
  namespace: {{ .Release.Namespace }}
  annotations:
    hooks.bigbang.dev/integration: "true"
    hooks.bigbang.dev/token: {{ $token | quote }}
spec: {{ toYaml $spec | nindent 2 }}
{{- $np := default dict $spec.networkPolicies -}}
{{- if and $np.enabled (or (dig "defaultsAsHooks" "enabled" false $np) (contains "\"helm.sh/hook\"" (toJson $np))) -}}
  {{- $ctx := deepCopy . -}}
  {{- $values := mergeOverwrite (dict "istio" dict "routes" dict) (deepCopy .Values) (deepCopy $spec) -}}
  {{- $_ := set $ctx "Values" $values -}}
  {{- $config := dig "globalConfig" dict $hooks -}}
  {{- $namespace := dig "namespace" "bigbang-operator" $config -}}
  {{- $name := dig "name" "bigbang-operator-global" $config -}}
  {{- $cm := dict -}}
  {{- if and $namespace $name -}}
    {{- $cm = lookup "v1" "ConfigMap" $namespace $name | default dict -}}
  {{- end -}}
  {{- range $direction := list "egress" "ingress" -}}
    {{- $raw := dig "data" (printf "%sDefinitions" $direction) "" $cm -}}
    {{- $shared := dict -}}
    {{- if $raw -}}
      {{- $shared = mustFromJson (mustToJson (fromYaml $raw)) -}}
      {{- if hasKey $shared "Error" -}}
        {{- fail (printf "invalid shared %s definitions: %s" $direction $shared.Error) -}}
      {{- end -}}
    {{- end -}}
    {{- $local := dig $direction dict $ctx.Values.networkPolicies -}}
    {{- $definitions := deepCopy $shared -}}
    {{- range $key, $definition := default dict $local.definitions -}}
      {{- $_ := set $definitions $key $definition -}}
    {{- end -}}
    {{- $_ := set $local "definitions" $definitions -}}
    {{- $_ := set $ctx.Values.networkPolicies $direction $local -}}
  {{- end -}}
  {{- $operations := dict -}}
  {{- $names := dict -}}
  {{- range $document := regexSplit "(?m)^---[ \t]*$" (include "bb-common.network-policies.render" $ctx) -1 -}}
    {{- $policy := fromYaml $document -}}
    {{- if eq (default "" $policy.kind) "NetworkPolicy" -}}
      {{- $annotations := dig "metadata" "annotations" dict $policy -}}
      {{- $phases := default "" (index $annotations "helm.sh/hook") -}}
      {{- if $phases -}}
        {{- $weight := dig "helm.sh/hook-weight" "-10" $annotations -}}
        {{- if not (regexMatch "^-?[0-9]+$" (toString $weight)) -}}
          {{- fail "helm.sh/hook-weight must be an integer" -}}
        {{- end -}}
        {{- if ge (int $weight) (int $cleanupWeight) -}}
          {{- fail "operatorHooks.cleanupWeight must exceed every hook policy weight" -}}
        {{- end -}}
        {{- $normalizedPhases := list -}}
        {{- range $phase := splitList "," $phases -}}
          {{- $phase = trim $phase -}}
          {{- if not (has $phase (list "pre-install" "post-install" "pre-upgrade" "post-upgrade" "pre-rollback" "post-rollback" "pre-delete" "post-delete")) -}}
            {{- fail (printf "unsupported NetworkPolicy hook phase %q" $phase) -}}
          {{- end -}}
          {{- $normalizedPhases = append $normalizedPhases $phase -}}
          {{- $_ := set $operations (trimPrefix "pre-" (trimPrefix "post-" $phase)) true -}}
        {{- end -}}
        {{- $name := printf "%s-hook-%s" ($.Release.Name | trunc 30 | trimSuffix "-") (printf "%s/%s" $.Release.Name $policy.metadata.name | sha256sum | trunc 16) -}}
        {{- if hasKey $names $name -}}
          {{- fail (printf "duplicate hook NetworkPolicy %s" $policy.metadata.name) -}}
        {{- end -}}
        {{- $_ := set $names $name true -}}
        {{- $_ := set $policy.metadata "name" $name -}}
        {{- $_ := set $policy.metadata "namespace" $.Release.Namespace -}}
        {{- $_ := unset $policy.metadata "ownerReferences" -}}
        {{- $labels := default dict $policy.metadata.labels -}}
        {{- $_ := unset $labels "bigbang.dev/package" -}}
        {{- $_ := set $labels "hooks.bigbang.dev/release" $.Release.Name -}}
        {{- $_ := set $policy.metadata "labels" $labels -}}
        {{- $_ := set $annotations "helm.sh/hook" (join "," (uniq $normalizedPhases)) -}}
        {{- $_ := set $annotations "helm.sh/hook-weight" (toString $weight) -}}
        {{- /* Retain policies across hook phases and until persistent rules are ready. */ -}}
        {{- $_ := set $annotations "helm.sh/hook-delete-policy" "before-hook-creation" -}}
        {{- $_ := set $annotations "hooks.bigbang.dev/token" $token -}}
        {{- $_ := set $annotations "hooks.bigbang.dev/timeout-seconds" (toString $timeout) -}}
        {{- $_ := set $policy.metadata "annotations" $annotations }}
---
{{ toYaml $policy }}
      {{- end -}}
    {{- end -}}
  {{- end -}}
  {{- range $phase, $_ := $operations }}
---
apiVersion: bigbang.dev/v1alpha1
kind: Package
metadata:
  name: {{ printf "%s-hook-cleanup-%s" $.Release.Name $phase }}
  namespace: {{ $.Release.Namespace }}
  annotations:
    helm.sh/hook: post-{{ $phase }}
    helm.sh/hook-weight: {{ $cleanupWeight | quote }}
    helm.sh/hook-delete-policy: before-hook-creation
    hooks.bigbang.dev/action: cleanup
    hooks.bigbang.dev/release: {{ $.Release.Name | quote }}
    hooks.bigbang.dev/token: {{ $token | quote }}
    hooks.bigbang.dev/phase: {{ $phase }}
    hooks.bigbang.dev/timeout-seconds: {{ $timeout | quote }}
spec: {}
  {{- end -}}
{{- end -}}
{{- end -}}
