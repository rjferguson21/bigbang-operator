#!/usr/bin/env bash
# Mirror generated CRD manifests from config/crd/bases into chart/templates/crds.
# Run after `make manifests` so the Big Bang chart ships the current CRD.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="${REPO_ROOT}/config/crd/bases"
DEST="${REPO_ROOT}/chart/templates/crds"

mkdir -p "${DEST}"
rm -f "${DEST}"/*.yaml
cp "${SRC}"/*.yaml "${DEST}/"
echo "synced $(ls "${DEST}" | wc -l) CRD file(s) to ${DEST}"
