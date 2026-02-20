#!/usr/bin/env bash
# scripts/licenses.sh — collect third-party license notices for all Go dependencies
#
# Called by GoReleaser before hooks. Writes THIRD_PARTY_LICENSES at repo root,
# with a clearly delimited section for each dependency.
set -euo pipefail

GOPATH="$(go env GOPATH)"
LICENSES_BIN="${GOPATH}/bin/go-licenses"

if [[ ! -x "${LICENSES_BIN}" ]]; then
    echo "Installing go-licenses..."
    go install github.com/google/go-licenses@latest
fi

SAVE_DIR="$(mktemp -d)"
trap 'rm -rf "${SAVE_DIR}"' EXIT

"${LICENSES_BIN}" save ./... --save_path "${SAVE_DIR}" --force

OUTPUT="THIRD_PARTY_LICENSES"
: > "${OUTPUT}"

while IFS= read -r -d '' file; do
    rel="${file#"${SAVE_DIR}/"}"
    module="$(dirname "${rel}")"
    printf '================================================================================\n' >> "${OUTPUT}"
    printf 'Module: %s\n' "${module}" >> "${OUTPUT}"
    printf '================================================================================\n' >> "${OUTPUT}"
    cat "${file}" >> "${OUTPUT}"
    printf '\n' >> "${OUTPUT}"
done < <(find "${SAVE_DIR}" -type f -print0 | sort -z)

echo "Generated ${OUTPUT}"
