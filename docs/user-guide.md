# pixsy User Guide

> A getting-started handbook for everyday users: no coding required — just follow the steps and your own image host will be up and running.

---

## Contents

1. [What is pixsy?](#1-what-is-pixsy)
2. [Installation (pick one of three)](#2-installation-pick-one-of-three)
   - [Option A: Windows PC (easiest — just double-click)](#option-a-windows-pc-easiest--just-double-click)
   - [Option B: A server (Linux) — recommended for real use](#option-b-a-server-linux--recommended-for-real-use)
   - [Option C: Docker (one-minute deploy if you know containers)](#option-c-docker-one-minute-deploy-if-you-know-containers)
3. [First use: register and log in](#3-first-use-register-and-log-in)
4. [Everyday use: upload, get links, manage images](#4-everyday-use-upload-get-links-manage-images)
5. [Admin panel](#5-admin-panel)
6. [Migrating from Lsky Pro](#6-migrating-from-lsky-pro)
7. [Backup and upgrade](#7-backup-and-upgrade)
8. [FAQ](#8-faq)

---

## 1. What is pixsy?

**pixsy is an "image host": you store your pictures on it, it gives you back a web link, and you paste that link anywhere — a blog, a forum, a newsletter editor — and the image shows up.**

The difference from free public image hosts: pixsy runs on **your own computer or server**, and your pictures stay on **your own disk** —

- No worries about a third-party host shutting down, throttling you, or stamping ads/watermarks on your images;
- One file (a ~12MB exe, or a ~25MB Docker image) **is** the whole site — no database to install;
- Supported formats: **JPEG / PNG / GIF (animated) / WebP / ICO**;
- Thumbnails are generated automatically on upload, so the library stays fast even with thousands of images;
- Identical images are deduplicated by content, so nothing is ever stored twice.

---

## 2. Installation (pick one of three)

> All three options produce exactly the same site. When you open `http://localhost:8080` and see the pixsy home page, you're done.

### Option A: Windows PC (easiest — just double-click)

Best for: personal use, LAN use, or just trying it out.

1. **Get the program**: download `pixsy_windows_amd64.zip` from the [Releases page](https://github.com/easy-open-meta/pixsy/releases) (or run `make package` in the repo to build it yourself).
2. **Unzip** it into a **permanent** folder, e.g. `D:\pixsy\`. Inside there's a single file: `pixsy-windows-amd64.exe`.
3. **Double-click to run**. The first launch triggers a Windows Firewall prompt —
   - Only using it on this computer → just click Cancel;
   - Want other devices (e.g. your phone) on the LAN to access it → check "Private networks" and click Allow access.
4. The program **automatically opens your browser** to the pixsy home page. If nothing pops up, visit `http://localhost:8080` yourself.

**Want others to access it?** pixsy listens on port 8080 by default. To change the port, create a text file named `pixsy.json` next to the exe with just one line:

```json
{ "port": 9000 }
```

Double-click the exe again and it now uses port 9000 (full config reference in [Appendix: configuration](#appendix-configuration)).

**Want it to start on boot?** Register it once with Windows Task Scheduler. In an administrator PowerShell (adjust the path to yours):

```powershell
schtasks /Create /TN pixsy /SC ONSTART /RU SYSTEM /TR "D:\pixsy\pixsy-windows-amd64.exe --addr :8080 --data D:\pixsy\data"
```

> Note: the program runs as a foreground console window. When you double-click it, **closing the window stops the service** — that's not a crash; just relaunch it.

### Option B: A server (Linux) — recommended for real use

Best for: a cloud VPS / NAS / Raspberry Pi that stays on 24/7 serving images for your blog.

1. **Upload and install** (run on the server):

```bash
# after uploading pixsy_linux_amd64.tar.gz to the server:
tar -xzf pixsy_linux_amd64.tar.gz           # extracts the pixsy executable
sudo useradd -r -s /usr/sbin/nologin pixsy  # dedicated low-privilege run account
sudo mkdir -p /opt/pixsy/data
sudo install -m 0755 pixsy /opt/pixsy/pixsy
sudo chown -R pixsy:pixsy /opt/pixsy
```

> On ARM devices (some NAS / Raspberry Pi), use `pixsy_linux_arm64.tar.gz` instead.

2. **Register it as a system service** (auto-start on boot, auto-restart on crash):

```bash
sudo cp deploy/pixsy.service /etc/systemd/system/pixsy.service
sudo systemctl daemon-reload
sudo systemctl enable --now pixsy
sudo systemctl status pixsy   # you want to see active (running)
```

3. **Verify**: open `http://<server-ip>:8080` in a browser, or run this on the server:

```bash
curl http://127.0.0.1:8080/api/v1/config
```

A JSON response (the site config) means everything works.

If you change the config (port, data directory, etc.) later, run `sudo systemctl daemon-reload` followed by `sudo systemctl restart pixsy`.

> **Security reminder**: if your server has a firewall / cloud security group (Alibaba Cloud, Tencent Cloud, AWS, …), remember to open port 8080 there, or the outside world can't reach the site.

### Option C: Docker (one-minute deploy if you know containers)

An image is published on Docker Hub: `yjj6731/pixsy`.

```bash
# Quickest: docker run
docker run -d -p 8080:8080 -v ./data:/app/data --restart unless-stopped yjj6731/pixsy

# Or use docker compose from this project's directory (builds the image locally)
docker compose up -d --build
```

Data (database + images) is persisted in the host's `./data` directory and survives image upgrades. Customize via environment variables (see the comments in `docker-compose.yml`); the two most common:

```yaml
environment:
  - PIXSY_BASE_URL=https://img.example.com   # public URL prefix for image links (set when behind a CDN domain)
  - PIXSY_JWT_SECRET=change-to-a-random-string # recommended to set explicitly in production
```

---

## 3. First use: register and log in

1. Open the pixsy home page (`http://localhost:8080` or your server's address).
2. Click **Register** in the top-right corner, fill in a **username**, **password**, and **confirm password**, then click Register.
3. On success you're logged in automatically and land on the upload page.

> ⭐ **Important**: **the first registered user automatically becomes the admin**. So on your own installation, register first — admin is yours. Everyone who registers afterwards is a regular user.

You can **turn off open registration** any time in the admin panel (see [Admin panel](#5-admin-panel)). Once closed, only existing accounts can log in — for personal use, it's a good idea to close it right after you register, so strangers can't upload to your host.

---

## 4. Everyday use: upload, get links, manage images

### 4.1 Uploading images

After logging in, the home page **is** the upload page:

1. Click the **Select images** button; hold Ctrl (or Shift) in the file picker to choose multiple files at once.
2. The page shows the current limits up top: **single image ≤ 20 MB · up to 10 per batch** (an admin can change both limits in the admin panel).
3. Each file gets its own row with its own progress bar; files are uploaded one by one and marked **Done** when finished.
   - Failures state the reason directly, e.g. "exceeds the 20 MB per-image limit".
   - Special case: if an image lives in a "files on demand" cloud folder (e.g. OneDrive with only a placeholder on disk), you'll be asked to sync the file back to local storage first.

Once uploaded, new pictures appear immediately in the **My images** list below.

### 4.2 Getting a link (the whole point!)

Every picture in "My images" is a card with:

| Card element | What it does |
| --- | --- |
| Thumbnail | Click to open the large preview |
| Filename / size · dimensions | e.g. `64.7 KB · 300×200` |
| **BBCode** button | Copies the BBCode link (used by many forums); the button briefly turns into "Copied" |
| **Markdown** button | Copies the Markdown link (blogs, notes, docs) |

The two links look like this (your image's real address is already filled in):

```
Markdown:  ![filename](http://your-domain/i/xxxx.png)
BBCode:    [img]http://your-domain/i/xxxx.png[/img]
```

**Just want the plain image URL?** Click the thumbnail to open the large preview, then click **Open original** — the picture opens in a new tab and the browser's address bar holds the plain image URL, ready to copy.

> 💡 Good to know: every link looks like `/i/a-string-of-characters.png`, where the string is computed from the image's content. **Rename the file or re-upload the same picture — identical content shares the same address and never wastes disk space.**

### 4.3 Preview and delete

- **Preview**: click a card's thumbnail to open a lightbox with the full-size image, filename, size, and dimensions. Click "Open original" to view it in a new tab, or "Close" to exit.
- **Delete**: click **Delete** on the card. A confirmation appears — "Delete …? This cannot be undone." Deletion is real (thanks to content-hash deduplication, the file only leaves the disk when no other record references the same content), and **there is no recycle bin** — think before you confirm.

### 4.4 When your library grows

- The list **paginates** automatically: Previous / Next buttons at the bottom, plus the current page number.
- **Admins** get an extra **Mine / All** switch above the list: "Mine" shows your own uploads, "All" browses every image on the site (which you can also manage/delete).

---

## 5. Admin panel

After logging in with the admin account, a **Admin panel** entry appears in the top-right navigation. Inside there are two sections.

### 5.1 Site settings

| Setting | Default | What it does |
| --- | --- | --- |
| Site name | `pixsy` | Shown in the top-left corner; call it whatever you like |
| Allow new registrations | On | When off, the register page is disabled and only existing accounts can log in (recommended for personal use) |
| Allow uploads | On | The site-wide upload master switch — a one-click emergency stop |
| Max image size (MB) | 20 | Largest single file allowed (1–1024) |
| Max batch size | 10 | Most files selectable in one go (1–50) |

Click **Save settings** and changes take effect **immediately — no restart needed**.

### 5.2 Behavior worth knowing

- All of the above settings survive restarts (they're stored in the database).
- The login endpoint is rate-limited against brute force: too many wrong attempts too quickly will be temporarily rejected — just wait a moment.

---

## 6. Migrating from Lsky Pro

Coming from Lsky Pro? No need to re-upload picture by picture — the admin panel has a built-in migration tool:

1. Go to **Admin panel → Migrate images from Lsky Pro**.
2. Fill in three fields:
   - **Lsky address**: your old host's URL, e.g. `https://img.example.com`;
   - **Lsky email** / **Lsky password**: the credentials you used to log into Lsky.
3. Click **Start migration**.

What happens next:

- Migration runs **in the background on the server**; **closing the browser does not stop it**. Return to the admin page any time to see the progress bar and the live "completed x / y" counter.
- Images are imported under **the currently logged-in admin account**.
- pixsy automatically records path aliases for the old site: **old `/i/…` links keep working after you switch the domain** — no need to edit old articles one by one.
- Individual failures never abort the whole migration; expand "Failure details" afterwards to see what went wrong.
- Once finished, you can download the **mapping CSV** (old link → new link) and batch-replace links in your articles with any find-and-replace tool.
- Click **Cancel migration** at any time to stop (whatever already transferred stays).

> Requirements: the pixsy server must be able to reach the old Lsky site, and the Lsky account must own the images. Prefer a terminal? The bundled script `scripts/migrate-lsky.mjs` does the same thing from the command line.

---

## 7. Backup and upgrade

### Backing up = copying one folder

Everything pixsy owns lives in the **data directory** (by default the `data\` folder next to the program; `/opt/pixsy/data` on Linux):

```
data/
├── pixsy.db      # database (accounts, image records, site settings)
├── jwt.secret    # login-token signing key
├── files/        # original images
└── thumbs/       # thumbnails
```

**Copy or zip this entire folder regularly and you have a full backup.** To restore, put it back in place.

### Upgrading

1. Back up the `data` directory (to be safe).
2. Replace the old program file (exe / binary / image) with the new version — **don't touch or delete the data directory**.
3. Start it again. Accounts, settings, and pictures all carry over.

---

## 8. FAQ

**Q: The page says "connection refused".**
A: The program isn't running. On Windows, double-click the exe and watch whether the console window flashes and disappears (port already taken? see next question). On Linux, check `systemctl status pixsy`.

**Q: Port 8080 is already in use.**
A: Put a `pixsy.json` next to the exe containing `{ "port": 9000 }`, restart, and the site moves to port 9000.

**Q: The upload button does nothing / says uploads aren't allowed.**
A: An admin turned off "Allow uploads" in the admin panel, or set the size limit too small.

**Q: I forgot the admin password.**
A: There's no password-recovery feature (all data is yours to manage). If registration is still open, register a fresh account and have someone comfortable with databases promote it to admin; otherwise restore from a backup of the `data` directory.

**Q: Images don't show on the site where I pasted the links.**
A: Check in order — ① the link is really the image address (starts with `/i/…`); ② your pixsy may only be reachable from your own machine/LAN, so visitors can't load it; ③ mixed-content blocking: your site is https but the image link is http, so the browser blocks it.

**Q: Can images go through a CDN / custom domain?**
A: Set `PIXSY_BASE_URL` at startup (or `base_url` in the config file) to your CDN domain; generated links will automatically carry that prefix.

**Q: What is that black console window on Windows?**
A: That's the program itself. Closing it stops the service. To run it as a background service, use the Task Scheduler method in section 2, or just use Docker.

---

## Appendix: configuration

Priority: **command-line flags > `PIXSY_*` environment variables > `pixsy.json` config file > defaults**.

Most people only need these:

| What you want | How |
| --- | --- |
| Change the port | `"port": 9000` in the config file, or `--addr :9000`, or `PIXSY_ADDR` |
| Where images are stored | `"data": "D:/picdata"` in the config file, or `--data` (default `./data` next to the program) |
| CDN / link prefix | `"base_url": "https://img.xxx.com"` in the config file, or `PIXSY_BASE_URL` |
| Disable auto-opening the browser | `"open_browser": false` in the config file, or `--open-browser=false` |
| Site settings (name, switches, limits) | Change them in the **web admin panel** first — instant effect. Anything written in the `settings` section of the config file **overrides the panel on every startup**; keys you leave out are untouched |

Full `pixsy.json` example:

```json
{
  "port": 8080,
  "data": "./data",
  "base_url": "",
  "jwt_secret": "",
  "static_dir": "",
  "open_browser": true,
  "settings": {
    "site_name": "pixsy",
    "allow_register": true,
    "allow_upload": true,
    "max_upload_mb": 20,
    "max_batch": 10
  }
}
```

> ⚠️ Leave `jwt_secret` empty and one is generated automatically into the data directory — normally you never touch it; set it explicitly only when running multiple instances/containers and you want logins to persist. Invalid values in `settings` (e.g. a limit out of range) make the program refuse to start with a clear reason — fix it as instructed and you're back in business.
