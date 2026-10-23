#!/bin/sh
# Runs paneterm end to end in Chromium on Linux, in Docker:
# panel page -> native messaging -> paneterm-host -> PTY -> zsh.
set -eu
cd "$(dirname "$0")/.."

case "$(docker info -f '{{.Architecture}}')" in
  aarch64 | arm64) arch=arm64 ;;
  *) arch=amd64 ;;
esac
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -o e2e/.bin/paneterm-host ./cmd/paneterm-host

docker build -q -t paneterm-e2e e2e >/dev/null
# pwd -W gives Docker a Windows path under Git Bash; elsewhere it fails.
root=$(pwd -W 2>/dev/null || pwd)
# --init reaps shells orphaned by a closed panel, as launchd does on macOS;
# without it they linger as zombies under node.
MSYS_NO_PATHCONV=1 docker run --rm --init \
  -v "$root/${EXTENSION_DIR:-extension}:/work/extension:ro" \
  -v "$root/e2e/.bin:/opt/paneterm:ro" \
  paneterm-e2e
