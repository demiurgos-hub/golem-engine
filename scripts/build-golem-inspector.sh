#!/usr/bin/env sh
# Builds the Golem Inspector UI and embeds it into the hub binary.
# Usage: scripts/build-golem-inspector.sh [output path]   (default bin/golem-inspector)
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out=${1:-"$root/bin/golem-inspector"}

cd "$root/golem-inspector"
npm ci --no-audit --no-fund
npm run build

cd "$root"
GOWORK=off CGO_ENABLED=0 go build -trimpath -o "$out" ./cmd/golem-inspector
echo "built $out"
