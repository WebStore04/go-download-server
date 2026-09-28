#!/bin/bash
# Rebuild, install, and restart the download server.
# From /opt/download-server:
#   chmod +x rebuild.sh
#   ./rebuild.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o download-server .
sudo install -m 0755 "$ROOT/download-server" /usr/local/bin/download-server
sudo systemctl restart download-server
sudo systemctl --no-pager --lines=20 status download-server
