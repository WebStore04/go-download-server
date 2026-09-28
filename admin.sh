#!/bin/bash
# Write the admin username and password, then restart the download server.
# From /opt/download-server:
#   chmod +x admin.sh
#   sudo ./admin.sh
#   sudo ./admin.sh admin 'choose-a-long-password'

set -euo pipefail

ENV_FILE=/var/lib/download-server/admin.env

if [[ "$(id -u)" -ne 0 ]]; then
	echo "run as root: sudo ./admin.sh" >&2
	exit 1
fi

if ! id download >/dev/null 2>&1; then
	echo "the download user is missing. Run sudo ./deploy/install.sh first." >&2
	exit 1
fi

user="${1:-}"
pass="${2:-}"

if [[ -z "$user" ]]; then
	read -r -p "Admin username [admin]: " user
	user="${user:-admin}"
fi

if [[ -z "$pass" ]]; then
	read -r -s -p "Admin password: " pass
	echo
	read -r -s -p "Repeat password: " pass2
	echo
	if [[ "$pass" != "$pass2" ]]; then
		echo "passwords do not match" >&2
		exit 1
	fi
fi

if [[ -z "$user" || -z "$pass" ]]; then
	echo "username and password are required" >&2
	exit 1
fi
if [[ "$user" == *$'\n'* || "$pass" == *$'\n'* || "$user" == *"="* ]]; then
	echo "username and password must be a single line, and the username cannot contain =" >&2
	exit 1
fi

install -d -o download -g download -m 0755 /var/lib/download-server
tmp="$(mktemp)"
printf 'ADMIN_USER=%s\nADMIN_PASS=%s\n' "$user" "$pass" > "$tmp"
install -m 0640 -o root -g download "$tmp" "$ENV_FILE"
rm -f "$tmp"

if systemctl cat download-server.service >/dev/null 2>&1; then
	systemctl restart download-server
	systemctl --no-pager --lines=20 status download-server
fi

echo
echo "admin login saved for user: $user"
echo "file: $ENV_FILE"
echo "page: http://YOUR_VPS_IP:8080/login"
