#!/usr/bin/env bash
# Regenerate api/v1alpha1/zz_generated.types.go from bb-common's values.schema.json.
#
# Inputs:
#   BB_COMMON_CHART  path to the bb-common chart directory
#                    (default: /home/rob/bb/bb-common/chart)
# Output:
#   api/v1alpha1/zz_generated.types.go
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BB_COMMON_CHART="${BB_COMMON_CHART:-/home/rob/bb/bb-common/chart}"
SCHEMA="${BB_COMMON_CHART}/values.schema.json"
OUT="${REPO_ROOT}/api/v1alpha1/zz_generated.types.go"
TOOL="${REPO_ROOT}/bin/go-jsonschema"

if [[ ! -f "${SCHEMA}" ]]; then
  echo "schema not found: ${SCHEMA}" >&2
  exit 1
fi
if [[ ! -x "${TOOL}" ]]; then
  echo "go-jsonschema not found at ${TOOL}; run: GOBIN=${REPO_ROOT}/bin go install github.com/atombender/go-jsonschema@latest" >&2
  exit 1
fi

# Copy schema to a renamed file so the generated root type is BBCommonValues.
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
cp "${SCHEMA}" "${WORK}/bbcommon_values.json"

"${TOOL}" \
  --package v1alpha1 \
  --only-models \
  --tags json \
  --disable-omitzero \
  --capitalization=ID,URL,DNS,HTTP,HTTPS,TCP,UDP,TLS,API,SNI,GRPC,CIDR,IP,YAML,JSON,SA \
  --output "${OUT}.tmp" \
  "${WORK}/bbcommon_values.json"

# Rename the verbose root prefix to clean per-subsystem names.
# BBCommonValuesIstio -> Istio, BBCommonValuesNetworkPolicies -> NetworkPolicies,
# BBCommonValuesRoutes -> Routes, BBCommonValuesSelfTest -> SelfTest,
# BBCommonValuesGlobal -> Global, and the root BBCommonValues -> bbCommonValues
# (kept lowercase so it's unexported -- we don't use it; PackageSpec wraps the
# three subsystems directly).
sed -i \
  -e 's/BbcommonValuesJSONIstio/Istio/g' \
  -e 's/BbcommonValuesJSONNetworkPolicies/NetworkPolicies/g' \
  -e 's/BbcommonValuesJSONRoutes/Routes/g' \
  -e 's/BbcommonValuesJSONSelfTest/SelfTest/g' \
  -e 's/BbcommonValuesJSONGlobal/Global/g' \
  -e 's/\bBbcommonValuesJSON\b/bbCommonValues/g' \
  "${OUT}.tmp"

# Replace all `interface{}` with `apiextensionsv1.JSON` — controller-gen
# refuses `interface{}` as a deep-copyable type. Then promote the
# port int-or-string field to the apimachinery intstr type.
sed -i \
  -e 's/interface{}/apiextensionsv1.JSON/g' \
  -e 's/Port apiextensionsv1.JSON `json:"port,omitempty"`/Port *intstr.IntOrString `json:"port,omitempty"`/' \
  "${OUT}.tmp"

# Extract anonymous struct values from `type X map[string]struct {...}` into
# their own named type `XValue`. controller-gen can't deepcopy anonymous
# struct map values.
python3 - "${OUT}.tmp" <<'PY'
import re, sys
path = sys.argv[1]
src = open(path).read()
lines = src.splitlines(keepends=True)
out = []
i = 0
while i < len(lines):
    line = lines[i]
    m = re.match(r'^type (\w+) map\[string\]struct \{\s*\n', line)
    if not m:
        out.append(line)
        i += 1
        continue
    name = m.group(1)
    value_name = f'{name}Value'
    # collect the struct body until the matching closing brace at column 0.
    body = []
    i += 1
    while i < len(lines) and lines[i].rstrip() != '}':
        body.append(lines[i])
        i += 1
    # i now points at the closing '}'
    i += 1
    # emit value type and the map alias.
    out.append(f'type {value_name} struct {{\n')
    out.extend(body)
    out.append('}\n')
    out.append('\n')
    out.append(f'type {name} map[string]{value_name}\n')
open(path, 'w').write(''.join(out))
PY

# Drop unused root-level types: the unexported bbCommonValues wrapper and
# its companions Global and SelfTest aren't part of PackageSpec.
python3 - "${OUT}.tmp" <<'PY'
import re, sys
path = sys.argv[1]
src = open(path).read()
# Strip a "type Name <struct|map>" block: from "type Name " to the next "^type " or EOF.
def strip_type(src, name):
    pattern = re.compile(
        r'(^|\n)(?://[^\n]*\n)*type ' + re.escape(name) + r'\b[^\n]*(?:\n(?:\{[^\n]*|[^\n]*))*?(?=\ntype |\Z)',
        re.MULTILINE,
    )
    return pattern.sub('', src)
# Simpler approach: line-by-line removal of declarations whose first line starts with the target name.
lines = src.splitlines(keepends=True)
out = []
skip = False
targets = {'bbCommonValues', 'Global', 'SelfTest'}
i = 0
while i < len(lines):
    line = lines[i]
    m = re.match(r'^type (\w+)\b', line)
    if m and m.group(1) in targets:
        # consume preceding comment block already emitted? remove the trailing comment lines we wrote.
        while out and out[-1].lstrip().startswith('//'):
            out.pop()
        # skip until next blank line that precedes another `type` or until next `type` declaration.
        # accept either single-line `type X = Y` / `type X map[...]` or multi-line struct.
        if '{' in line and '}' not in line:
            # multi-line struct: skip through matching closing brace line
            i += 1
            depth = 1
            while i < len(lines) and depth > 0:
                depth += lines[i].count('{') - lines[i].count('}')
                i += 1
            # also skip a trailing blank line
            if i < len(lines) and lines[i].strip() == '':
                i += 1
        else:
            i += 1
            if i < len(lines) and lines[i].strip() == '':
                i += 1
        continue
    out.append(line)
    i += 1
open(path, 'w').write(''.join(out))
PY

# Replace the auto-generated header import block (only "package v1alpha1")
# with the package decl plus the imports we now need.
python3 - "${OUT}.tmp" <<'PY'
import sys
path = sys.argv[1]
src = open(path).read()
header = (
    '// Code generated by github.com/atombender/go-jsonschema, DO NOT EDIT.\n'
    '// Post-processed by hack/schema-to-go.sh.\n'
    '\n'
    'package v1alpha1\n'
    '\n'
    'import (\n'
    '\tapiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"\n'
    '\t"k8s.io/apimachinery/pkg/util/intstr"\n'
    ')\n'
)
# Drop the generator's original header (everything up to the first `type ` declaration).
idx = src.find('\ntype ')
if idx < 0:
    raise SystemExit('no type declarations found')
open(path, 'w').write(header + src[idx:])
PY

mv "${OUT}.tmp" "${OUT}"
gofmt -w "${OUT}"
echo "wrote ${OUT}"
