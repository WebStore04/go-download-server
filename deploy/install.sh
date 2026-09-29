#!/bin/bash
# Install the download server on a systemd VPS (Ubuntu or Debian).
# From the download-server directory, on the VPS:
#   sudo ./deploy/install.sh
#
# Go is required on the machine that runs this script. To build elsewhere:
#   CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o download-server .
# Copy that binary into this directory, then run the script on the VPS.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ "$(id -u)" -ne 0 ]]; then
	echo "run as root: sudo ./deploy/install.sh" >&2
	exit 1
fi

if command -v go >/dev/null 2>&1; then
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$ROOT/download-server" .
elif [[ ! -x "$ROOT/download-server" ]]; then
	echo "Go is not installed and $ROOT/download-server is missing." >&2
	echo "Build it on another machine, copy the binary here, and rerun:" >&2
	echo "  CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o download-server ." >&2
	exit 1
fi

if ! id download >/dev/null 2>&1; then
	useradd --system --user-group --home-dir /var/lib/download-server --shell /usr/sbin/nologin download
fi

install -m 0755 "$ROOT/download-server" /usr/local/bin/download-server
install -d -o download -g download -m 0755 /var/lib/download-server/files /var/log/download-server

if [[ -f "$ROOT/files/sample.txt" ]] && [[ ! -e /var/lib/download-server/files/sample.txt ]]; then
	install -m 0644 -o download -g download "$ROOT/files/sample.txt" /var/lib/download-server/files/sample.txt
fi

if [[ ! -f /var/lib/download-server/admin.env ]]; then
	pass="$(openssl rand -base64 18)"
	printf 'ADMIN_USER=admin\nADMIN_PASS=%s\n' "$pass" > /var/lib/download-server/admin.env
	chown root:download /var/lib/download-server/admin.env
	chmod 640 /var/lib/download-server/admin.env
	echo "admin login created: admin / $pass"
	echo "saved in /var/lib/download-server/admin.env"
fi

install -m 0644 "$ROOT/deploy/download-server.service" /etc/systemd/system/download-server.service
systemctl daemon-reload
systemctl enable --now download-server.service

echo
echo "files: /var/lib/download-server/files"
echo "log:   /var/log/download-server/downloads.log"
echo "db:    /var/lib/download-server/downloads.db"
echo "page:  http://YOUR_VPS_IP:8080/login"
echo "copy files into that directory. They must be readable by the download user (chmod 644 is enough)."
echo "files in that directory download with curl. A browser request is refused."
echo "curl -O http://YOUR_VPS_IP:8080/files/sample.txt"
echo "if ufw is enabled: ufw allow 8080/tcp"
echo "journal: journalctl -u download-server -f"
