#!/usr/bin/env bash
# scripts/completions.sh — generate shell completion scripts
#
# Called by GoReleaser before hooks. Writes completion files to completions/
# so they can be included in release archives.
set -euo pipefail

mkdir -p completions

go run ./cmd/dotsmith shell bash > completions/dotsmith.bash
go run ./cmd/dotsmith shell zsh  > completions/dotsmith.zsh
go run ./cmd/dotsmith shell fish > completions/dotsmith.fish
