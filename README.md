# Download server

A small Go service that serves files for download and records each finished download. Public files stay open. The log page and the project file list need an admin login.

Source directory on the VPS: `/opt/download-server`

Live pages, replace the IP with the VPS address:

- `http://216.126.239.166:8080/` shows `go is on the service`
- `http://216.126.239.166:8080/files/sample.txt` is a public download
- `http://216.126.239.166:8080/login` is the admin login
- `http://216.126.239.166:8080/logs` is the download log (admin)
- `http://216.126.239.166:8080/projects` lists admin-only project files (admin)

GitHub: https://github.com/WebStore04/go-download-server

## Install Go

Use the official tarball. The `apt` Go package is older than this project needs (Go 1.22 or newer).

On an amd64 VPS:

```bash
cd /tmp
curl -fsSL -o go.tgz https://go.dev/dl/go1.27.1.linux-amd64.tar.gz
echo "63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445  go.tgz" | sha256sum -c
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go.tgz
sudo ln -sf /usr/local/go/bin/go /usr/local/bin/go
go version
```

`sha256sum` must print `go.tgz: OK` before you unpack. On arm64, download `go1.27.1.linux-arm64.tar.gz` and expect SHA256 `3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec`.

## Install the service

Put these files in `/opt/download-server` (paste them, or clone the GitHub repo into that path):

```text
go.mod
go.sum
main.go
server.go
auth.go
db.go
logfmt.go
rebuild.sh
admin.sh
.gitignore
README.md
files/sample.txt
deploy/install.sh
deploy/download-server.service
```

The first build downloads the SQLite module, so the VPS needs network access to the Go module proxy.

```bash
cd /opt/download-server
chmod +x deploy/install.sh admin.sh rebuild.sh
sudo ./deploy/install.sh
```

That script builds the binary, creates the `download` user, installs `/usr/local/bin/download-server`, and starts `download-server.service` on port 8080.

If `ufw` is on:

```bash
sudo ufw allow 8080/tcp
```

Check it:

```bash
systemctl status download-server
curl -s http://127.0.0.1:8080/
```

Paths the service uses:

| Path | What it is |
| --- | --- |
| `/var/lib/download-server/files` | Public downloads. Put files here, not under `/opt/download-server/files`. |
| `/var/www/html/git-projects` | Admin-only project files, including files inside folders. |
| `/var/lib/download-server/downloads.db` | SQLite log used by `/logs` |
| `/var/log/download-server/downloads.log` | Same events, as readable text |
| `/var/lib/download-server/admin.env` | Admin username and password |
| `/var/lib/download-server/session.key` | Login cookie key, created on first start |

Public file example:

```bash
sudo cp myfile.txt /var/lib/download-server/files/
sudo chown download:download /var/lib/download-server/files/myfile.txt
sudo chmod 644 /var/lib/download-server/files/myfile.txt
```

Project files. The server does not create this directory. The `download` user must be able to read it:

```bash
sudo mkdir -p /var/www/html/git-projects
sudo apt install -y acl
sudo setfacl -R -m u:download:rX /var/www/html/git-projects
sudo setfacl -R -d -m u:download:rX /var/www/html/git-projects
sudo -u download ls -R /var/www/html/git-projects
```

## Add the admin username and password

`install.sh` writes `/var/lib/download-server/admin.env` the first time, with username `admin` and a random password. It prints that password once. To choose the username and password yourself:

```bash
cd /opt/download-server
sudo ./admin.sh
```

Or pass them on the command line. Use a long password. Single quotes keep the shell from eating special characters:

```bash
sudo ./admin.sh admin 'choose-a-long-password'
```

The script writes the file as mode `640`, owner `root`, group `download`, then restarts the service. Mode `600` owned by `root:root` makes login fail, because the service user cannot read the file.

The journal must contain `admin password: set`. It never prints the password.

```bash
sudo journalctl -u download-server -n 30 --no-pager
```

Then open `http://216.126.239.166:8080/login`. If the page says `Admin login is not configured.`, the password file is missing, empty, or unreadable, and the service was not restarted after it was fixed.

Raw text log:

```bash
sudo tail -f /var/log/download-server/downloads.log
```

## Rebuild

After you change the Go source in `/opt/download-server`:

```bash
cd /opt/download-server
./rebuild.sh
```

The script builds with `CGO_ENABLED=0`, installs the binary to `/usr/local/bin/download-server`, and restarts the service.

If you also changed `deploy/download-server.service`, copy it before the rebuild:

```bash
sudo cp deploy/download-server.service /etc/systemd/system/download-server.service
sudo systemctl daemon-reload
./rebuild.sh
```

## Push to GitHub

Remote: https://github.com/WebStore04/go-download-server

Run this from `/opt/download-server`, the directory that contains `main.go`. The admin password, session key, database, and text log live outside that directory. Leave them there. `.gitignore` already skips the binary, `downloads.db`, and `downloads.log`.

Install git if it is missing, then publish the first copy. The GitHub repo starts empty, so this first push creates `main`.

```bash
sudo apt install -y git
cd /opt/download-server
git init
git add .
git status
git commit -m "Add the download server"
git branch -M main
git remote add origin https://github.com/WebStore04/go-download-server.git
git push -u origin main
```

GitHub asks for a username and a password. The username is `WebStore04`. The password is a personal access token with access to this repository (GitHub, Settings, Developer settings, Personal access tokens). The account password is rejected by `git push`.

If `origin` already points somewhere else:

```bash
git remote set-url origin https://github.com/WebStore04/go-download-server.git
```

After later edits on the VPS:

```bash
cd /opt/download-server
git add .
git status
git commit -m "Describe the change"
git push
```

On another machine:

```bash
git clone https://github.com/WebStore04/go-download-server.git
```
