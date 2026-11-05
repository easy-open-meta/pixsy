# pixsy

> A lightweight self-hosted image hosting service: single binary, zero external dependencies, ready out of the box.

![Version](https://img.shields.io/badge/version-1.0.0-4F46E5)
![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)
![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vuedotjs&logoColor=white)
![SQLite](https://img.shields.io/badge/SQLite-embedded-003B57?logo=sqlite&logoColor=white)
![Docker](https://img.shields.io/badge/docker-yjj6731%2Fpixsy-2496ED?logo=docker&logoColor=white)
![Platform](https://img.shields.io/badge/platform-linux%20%7C%20windows-6B7280)
![License](https://img.shields.io/badge/license-MIT-16A34A)

[简体中文](README.md) | English

## Introduction

pixsy is a self-hosted image hosting service written in Go + Vue 3. The frontend build output is embedded directly into the backend binary via `go:embed`, and SQLite is accessed through a pure-Go driver (no CGO). The final deliverable is a single executable — copy it to your server and run it. No database installation, no external services.

## Features

- **Single-binary deployment** — the frontend is embedded via `go:embed`; one executable is the whole site
- **One-command Docker deployment** — multi-stage build on top of alpine, final image ~25MB; `docker compose up -d --build` and you are done
- **Zero external dependencies** — SQLite (pure-Go driver, no CGO) + local disk storage; cross-compilation friendly
- **Content-addressed dedup** — images are stored by SHA-256 hash, so identical content is stored only once; deleting an image keeps the file on disk while other records still reference it
- **Automatic thumbnails** — a JPEG thumbnail (max side 350px) is generated on upload for faster listing pages
- **Multiple formats** — JPEG / PNG / GIF / WebP / ICO
- **Seamless old links** — migrating from Lsky Pro records old path aliases automatically, so existing `/i/…` links in articles keep working after switching domains
- **Multiple link formats** — one-click copy as URL / Markdown / HTML / BBCode
- **Users & permissions** — JWT authentication; the first registered user automatically becomes the admin; the login endpoint is rate-limited
- **Online admin panel** — site name, open registration, open upload, per-file and per-batch limits; changes take effect immediately, no restart needed
- **Direct image serving** — supports HTTP Range requests for correct browser and downloader behavior

## Requirements

| Component | Version | Purpose |
| --- | --- | --- |
| Go | 1.22+ | Build the backend |
| Node.js + npm | 18+ | Build the frontend (build time only) |
| Docker | 20+ (optional) | Containerized deployment (build & run) |

## Quick Start

```bash
# 1. Build the frontend (output goes to web/dist, embedded via go:embed)
npm --prefix web install
npm --prefix web run build

# 2. Build the backend
go build -o pixsy ./cmd/pixsy

# 3. Run (listens on :8080 by default)
./pixsy
```

Open <http://localhost:8080> and you are ready to go. **The first registered user automatically becomes the admin**; log in and open the admin panel to adjust site settings.

Data (the SQLite database and image files) is stored under `data/` in the working directory by default; change it with `--data`.

## Deployment

### Cross-compilation

A Makefile at the repo root provides a unified build entry (targets build the frontend assets first):

```bash
make linux    # Cross-compile static single binaries for linux-amd64 / linux-arm64 into bin/
make windows  # Cross-compile windows-amd64 into bin/
make all      # All distribution artifacts
```

The artifacts are static single binaries built with `CGO_ENABLED=0` (SQLite uses a pure-Go driver, no libc dependency): the Linux binaries run on any distribution, and the Windows binary is covered below. Without make installed, run the equivalent commands manually:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-linux-amd64 ./cmd/pixsy
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-linux-arm64 ./cmd/pixsy
```

### Docker

The image is built in multiple stages (frontend → static backend binary → alpine runtime); the final image is about 25MB:

```bash
# Option 1: docker compose (recommended; includes the data volume and restart policy)
docker compose up -d --build

# Option 2: docker build + docker run
docker build -t pixsy .
docker run -d -p 8080:8080 -v ./data:/app/data pixsy
```

Data (the SQLite database and image files) is persisted in `./data` on the host and survives image upgrades. Pass `PIXSY_*` environment variables to customize the listen port, base URL or JWT secret (see the comments in `docker-compose.yml` and the configuration table below).

### Linux (systemd)

1. Upload the binary and create a dedicated service user plus the data directory:

```bash
sudo useradd -r -s /usr/sbin/nologin pixsy
sudo mkdir -p /opt/pixsy/data
sudo install -m 0755 bin/pixsy-linux-amd64 /opt/pixsy/pixsy
sudo chown -R pixsy:pixsy /opt/pixsy
```

2. Install and start the service (unit file: `deploy/pixsy.service`):

```bash
sudo cp deploy/pixsy.service /etc/systemd/system/pixsy.service
sudo systemctl daemon-reload
sudo systemctl enable --now pixsy
sudo systemctl status pixsy   # expect active (running)
```

The service listens on `:8080` and stores data in `/opt/pixsy/data` by default; re-run `sudo systemctl daemon-reload` after changing `ExecStart` arguments or environment variables.

### Windows

`make windows` produces `bin/pixsy-windows-amd64.exe` (a ~12MB single file with the frontend embedded). Copy it anywhere on the target machine and run it directly — no dependencies to install:

```powershell
# Run in the foreground (listens on :8080 by default; data goes to .\data)
.\pixsy-windows-amd64.exe

# Custom port and data directory
.\pixsy-windows-amd64.exe --addr :8080 --data D:\pixsy\data

# Or drop pixsy.json next to the exe (port and site settings live there) and just double-click
# See pixsy.json.example
```

On Windows the page opens in your default browser after startup (disable with `--open-browser=false` or `"open_browser": false` in the config file).

Without make installed, cross-compile manually on any machine that has Go:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-windows-amd64.exe ./cmd/pixsy
```

Notes:

- **Firewall**: on first run Windows shows a firewall prompt — allow access on private networks; for local-only use you can simply cancel it.
- **Start on boot**: the program is a foreground process; register it with Task Scheduler to start at boot (running as SYSTEM in the background):

```powershell
schtasks /Create /TN pixsy /SC ONSTART /RU SYSTEM /TR "D:\pixsy\pixsy-windows-amd64.exe --addr :8080 --data D:\pixsy\data"
```

- **Data directory**: the SQLite database, `jwt.secret` and image files all live inside the `--data` directory — backing it up backs up the whole site; uninstalling is just deleting the executable and the data directory.

### Smoke check

Once the service is up, `curl http://127.0.0.1:8080/api/v1/config` should return the site configuration JSON (native Linux corresponds to design doc TC-DEPLOY-001, Windows to TC-DEPLOY-002, Docker deployment to TC-DEPLOY-003).

## Configuration

Priority: **command-line flag > `PIXSY_*` environment variable > configuration file > default value**.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--config` | `PIXSY_CONFIG` | empty (auto-loads `pixsy.json` from the current directory) | Configuration file path (JSON) |
| `--addr` | `PIXSY_ADDR` | `:8080` | HTTP listen address |
| `--data` | `PIXSY_DATA` | `./data` | Data directory (SQLite and image files) |
| `--base-url` | `PIXSY_BASE_URL` | empty | Prefix for external image links (e.g. a CDN domain); empty means generated from the request Host |
| `--max-upload-mb` | `PIXSY_MAX_UPLOAD_MB` | `20` | Default per-file limit (MB) seeded into settings on first start; adjustable at runtime |
| `--jwt-secret` | `PIXSY_JWT_SECRET` | empty | JWT signing secret; if empty, one is generated and stored in the data directory |
| `--static-dir` | `PIXSY_STATIC_DIR` | empty | Frontend static assets directory; empty uses the embedded build (for dev hot reload) |
| `--open-browser` | `PIXSY_OPEN_BROWSER` | `true` on Windows, `false` elsewhere | Open the page in the default browser after startup; disable with `--open-browser=false` |

### Configuration file (pixsy.json)

Without `--config`, the program automatically loads `pixsy.json` from the **current directory** (ignored if absent). On Windows, double-clicking the exe or running it from Task Scheduler makes the working directory the exe's directory, so simply place the file next to the exe — no startup arguments needed. See `pixsy.json.example`.

Minimal config to change the port:

```json
{ "port": 8090 }
```

Full example:

```json
{
  "port": 8080,
  "data": "./data",
  "base_url": "",
  "jwt_secret": "",
  "static_dir": "",
  "settings": {
    "site_name": "pixsy",
    "allow_register": true,
    "allow_upload": true,
    "max_upload_mb": 20,
    "max_batch": 10
  }
}
```

| Key | Description |
| --- | --- |
| `port` | Listen port (`1~65535`), equivalent to `addr: ":<port>"`; mutually exclusive with `addr` |
| `addr` | Full listen address, e.g. `":8080"` or `"127.0.0.1:8080"` |
| `data` | Data directory |
| `base_url` | Prefix for external image links (e.g. a CDN domain) |
| `jwt_secret` | JWT signing secret; generated automatically if empty |
| `static_dir` | Frontend static assets directory; empty uses the embedded build |
| `open_browser` | Open the browser after startup; `true` by default on Windows, `false` elsewhere |
| `settings.*` | Site settings from the admin panel; meanings and defaults under "Site settings" below |

Semantics of `settings`: **every key present in the file overrides the admin panel on each startup**; keys you leave out are untouched (values edited in the panel survive restarts). Invalid values (e.g. an out-of-range `max_upload_mb`) make startup fail with a clear error instead of being silently ignored. Unknown keys in the file are rejected as well.

### Data directory layout

```
data/
├── pixsy.db      # SQLite database
├── jwt.secret    # JWT signing secret (auto-generated unless --jwt-secret is set)
├── files/        # Original images, named by content hash
└── thumbs/       # Thumbnails (<hash>.jpg)
```

### Site settings (admin panel)

Runtime settings live in the database and take effect immediately:

| Setting | Default | Description |
| --- | --- | --- |
| `site_name` | `pixsy` | Site name (1–64 characters) |
| `allow_register` | on | Whether registration is open |
| `allow_upload` | on | Whether uploading is open |
| `max_upload_mb` | `20` | Per-file size limit (1–1024 MB) |
| `max_batch` | `10` | Max number of files per batch upload (1–50) |

## Usage

### Web UI

- **Home**: multi-select upload with per-file progress, paginated browsing of "My images" / "All images" (admin only), one-click link copying, deletion
- **Admin panel** (admin): site name, toggles and limits; visual Lsky Pro migration (progress, cancel, URL-mapping CSV)

### API

Every endpoint returns a unified envelope: success is `{"data": ...}`, failure is `{"error":{"code":"...","message":"..."}}`. Authenticated endpoints expect an `Authorization: Bearer <token>` header.

| Method | Path | Description | Auth |
| --- | --- | --- | --- |
| GET | `/api/v1/config` | Public site config (site name, toggles, limits) | - |
| POST | `/api/v1/auth/register` | Register (first user becomes admin; can be disabled) | - |
| POST | `/api/v1/auth/login` | Log in, returns a JWT | - |
| GET | `/api/v1/auth/me` | Current user | Bearer |
| POST | `/api/v1/images` | Upload images, multipart field `file` (repeat for batch upload) | Bearer |
| GET | `/api/v1/images` | List images; supports `page` / `page_size` / `scope=mine\|all` (`all` is admin-only) | Bearer |
| DELETE | `/api/v1/images/{id}` | Delete an image | Bearer |
| GET | `/api/v1/admin/settings` | View site settings | Admin |
| PUT | `/api/v1/admin/settings` | Update site settings | Admin |
| POST | `/api/v1/admin/migrate/lsky` | Start an Lsky Pro migration (JSON: `base_url` / `email` / `password`) | Admin |
| GET | `/api/v1/admin/migrate/lsky` | Migration progress (poll) | Admin |
| POST | `/api/v1/admin/migrate/lsky/cancel` | Cancel the migration | Admin |
| GET | `/api/v1/admin/migrate/lsky/csv` | Download the URL-mapping CSV (old → new) | Admin |
| GET | `/i/{hash}.{ext}` | Serve the original image (Range supported); Lsky legacy path aliases `/i/{pathname}` created by migration work as well | - |
| GET | `/t/{hash}.jpg` | Serve the thumbnail | - |

#### curl examples

```bash
BASE=http://localhost:8080

# Register (the first registered user automatically becomes the admin)
curl -sX POST $BASE/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"change-me-123"}'

# Log in to get a token; the response looks like {"data":{"token":"…","user":{…}}}
curl -sX POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"change-me-123"}'

# Upload an image
curl -sX POST $BASE/api/v1/images \
  -H "Authorization: Bearer $TOKEN" \
  -F 'file=@cat.png'
# => {"data":{"images":[{"id":1,"url":"…/i/<hash>.png","markdown":"…","html":"…","bbcode":"…", …}],"total":1}}

# List your own images (admins can append &scope=all to see everything)
curl -s "$BASE/api/v1/images?page=1&page_size=20" -H "Authorization: Bearer $TOKEN"

# Delete an image
curl -sX DELETE $BASE/api/v1/images/1 -H "Authorization: Bearer $TOKEN"
```

## Development

```bash
# Backend tests
go test ./...

# Frontend dev server (http://localhost:5173; /api, /i and /t are proxied to 127.0.0.1:8080)
npm --prefix web install
npm --prefix web run dev

# In another terminal, run the backend
go run ./cmd/pixsy
```

Frontend hot reload: start the backend with `PIXSY_STATIC_DIR=web/dist` so static assets are read from disk, then develop against the Vite dev server in parallel.

Frontend type checking: `npm --prefix web run typecheck`.

### Project structure

```
pixsy/
├── cmd/pixsy/        # Entry point
├── internal/
│   ├── api/          # REST API handlers
│   ├── auth/         # JWT auth, middleware, login rate limiting
│   ├── config/       # Startup config (flag > env > config file > defaults)
│   ├── httpx/        # Unified JSON response / error envelope
│   ├── model/        # SQLite data access
│   ├── server/       # Routing, static assets and image serving
│   ├── setting/      # Site settings (settings table + in-memory cache)
│   └── storage/      # File & thumbnail storage, content-addressed dedup
├── web/              # Vue 3 + TypeScript frontend (dist/ embedded via go:embed)
├── Dockerfile        # Multi-stage build: frontend → static backend binary → alpine runtime
├── docker-compose.yml # Container orchestration example (data volume mounted at ./data)
└── docs/             # Design documents
```

## Documentation

- [Design document](docs/design.md) — architecture, data model and API contract (Chinese)
- [Design review alignment](docs/review-alignment.md) — item-by-item design-vs-implementation record (Chinese)
