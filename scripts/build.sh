#!/usr/bin/env bash
# Gera os binários do qrdrop para Windows, Linux e macOS em dist/.
#
# Uso (no Linux, macOS ou Git Bash do Windows):
#   ./scripts/build.sh          # versão = tag ou commit atual
#   ./scripts/build.sh v1.0.0   # versão escolhida
#
# É a compilação cruzada do Go: de qualquer sistema saem os binários de todos,
# só trocando GOOS e GOARCH. CGO_ENABLED=0 deixa cada binário autossuficiente
# (não depende de bibliotecas do sistema).
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
TARGETS="windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

mkdir -p dist
echo "qrdrop $VERSION"
for target in $TARGETS; do
  goos="${target%/*}"
  goarch="${target#*/}"
  out="dist/qrdrop-$goos-$goarch"
  [[ "$goos" == windows ]] && out="$out.exe"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$out" ./cmd/qrdrop
  echo "  $out ($(du -h "$out" | cut -f1))"
done
