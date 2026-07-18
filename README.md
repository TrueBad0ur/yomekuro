# yomekuro

[Русский](README-russian.md)

Self-hosted EPUB library for Japanese light novels and manga. Single binary + PostgreSQL. No OAuth, no external metadata providers — everything from EPUB files directly.

Includes a companion **converter** that turns manga image folders into fixed-layout EPUBs with OCR text overlay for [Yomitan](https://github.com/themoeway/yomitan).

**Live demo:** https://yomekuro.kubehomelab.space — log in with `test` / `test` to look around.

---

## Quick start

```bash
cp .env.example .env
# edit .env — set POSTGRES_PASSWORD and YOMEKURO_ADMIN_PASSWORD

docker compose up -d --build
```

`docker-compose.yml` is a symlink to `docker-compose.dev.yml` — it builds every
image from source (yomekuro + the converter services) on your machine. For a
production host that should just run a released version without a Go/Python
toolchain, use `docker-compose.prod.yml` instead, which pulls pre-built images
from Docker Hub:

```bash
cp .env.example .env
# edit .env — set POSTGRES_PASSWORD, YOMEKURO_ADMIN_PASSWORD, and the two
# image tags to pull: YOMEKURO_VERSION and CONVERTER_VERSION

docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
```

`yomekuro` and `converter` are versioned and pushed independently (`YOMEKURO_VERSION`
picks `truebad0ur/yomekuro:<tag>`, `CONVERTER_VERSION` picks
`truebad0ur/converter:gpu-<tag>`) — bump one without forcing a rebuild/pull of the
other when only one side actually changed. See "Releasing" below for how versions
get published to Docker Hub.

The login page also has a "Register" tab — anyone who can reach the server
gets a non-admin, read-only account; fine on a private network, worth knowing
before exposing this to the open internet.

Mounts a single `./library` — with two subfolders inside, `books/` and `manga/`
(one series per subfolder, `.epub` files inside; `books/` also holds
standalone `.html` files directly, one file = one book). Both are registered
as separate libraries and scanned automatically on boot. Open
http://localhost:8080 and log in.

The dev compose also brings up the converter services (`converter`,
`converter-gpu`, `converter-worker` — see "Converter" below) via
`docker-compose.dev.yml`'s `include:`. `converter/docker-compose.yml` still
works standalone if you only want the converter without yomekuro.

---

## Using yomekuro

### Library page

The home page lists your series as cover-art tiles. A centered tab row above
the grid switches between "All Titles" and each library (Books / Manga);
click a series to see its books, click a book to start reading. Search and
genre/tag filters are in the header.

Each book has a **⋯** menu (top-right of its cover) to **mark the volume read or
unread** — handy when you finish a book somewhere other than its last page.
Finished volumes show "Read" instead of a percentage; the bar tracks reading
progress otherwise. Admins get an extra "Edit genres" entry in the same menu.

![Library page](docs/screenshots/library.png)

### Reading

Manga opens in fixed-layout page view (with a **Spread** toggle for
two-page spreads); novels open in a scrolling or vertical (RTL) layout.
Yomitan works directly against the OCR text overlay on manga pages — no
iframe getting in the way. See [Reader](#reader) below for keyboard
shortcuts.

On a phone, swipe or tap the left/right edge of the screen to turn manga
pages — text selection (for bookmarking) only kicks in on a genuine
long-press, so a plain tap or swipe never leaves a stray highlight behind. A
page photographed as one fused two-page spread (rather than a photo per
physical page) shows one half at a time by default; press **Spread** to see
the whole original image instead.

![Reader](docs/screenshots/reader.png)

### Bookmarks

Select text while reading to highlight it as a bookmark; selections are
saved per-book and stay put on furigana-heavy pages (`<ruby>`/`<rt>`) since
only individual text nodes get wrapped, never whole elements.

![Bookmarks](docs/screenshots/bookmarks.png)

### Settings (admin only)

Regular users only get the theme toggle and logout button in the header.
Admins additionally get a Settings page for managing libraries, users, and
uploading manga for OCR conversion.

![Settings page](docs/screenshots/settings.png)

### Upload & Jobs (admin only)

Settings → Upload & Jobs: pick a library, drop one or more archives of raw page
images, PDFs, and/or standalone `.html` files onto the upload area, and a
name. **Several files can be selected at once** — each archive/PDF becomes
its own conversion job and they queue up; an `.html` file needs no
conversion at all, it's just copied straight into the library and shows up
within seconds.

Tick **"Add to an existing book"** to drop extra volumes into a book already in
the library instead of creating a new one; several volumes may be queued for the
same book at the same time, including while its original conversion still runs.

Jobs are listed live on the same page with their current volume, and can be
stopped (or removed once finished). **Pause Queue** / **Resume Queue** pause
every queued job except whichever one is actively converting right now (which
runs to completion) — unlike Stop, pause never touches any file, so a whole
backlog can be parked for hours (e.g. to let the host cool down) and picked
back up exactly where it left off.

Every upload's raw, pre-OCR scan is also mirrored into `./backup/<library>/<name>/`
if a backup dir is configured (`YOMEKURO_BACKUP_DIR`, mounted by default in both
compose files) — a safety net independent of the conversion pipeline, since OCR
can be redone from a raw scan but a lost raw scan is gone for good.

![Upload/conversion log](docs/screenshots/conversion-log.png)

### Manage Books (admin only)

Settings → Manage Books lists every book already in a library, per volume:

- **Reconvert (full OCR)** — re-runs OCR from scratch (not a cache-reuse
  rebuild) for one volume or the whole book, at a chosen text-detector
  resolution (2048/3072/3584 — higher catches more but needs more GPU VRAM).
  Only available while the book's original raw scan (`<name>-in/`) is still
  on disk — shows "no raw scan" otherwise. A **⚠ raw scan changed** badge
  appears on any volume whose raw-scan files were modified on disk (page
  reordered/replaced by hand) more recently than its last conversion, so a
  stale EPUB is easy to spot without comparing timestamps yourself.
- **Download** — pulls a volume back out as a page-**Images** `.zip`, a
  **PDF**, or the raw **EPUB**, straight from the already-built file, no
  raw scan required. Handy for testing the upload flow again without
  re-sourcing the original scan. A ranobe/plain-EPUB volume (no page images
  at all) only offers an EPUB download; a standalone HTML-library book
  offers an HTML download.
- **Rename** — overrides the book's display name (`books.series_name` in the
  DB only, never the file or folder on disk) — the only way to change what a
  manga/PDF-derived book shows as, since its series name is otherwise baked
  into each volume's own EPUB metadata at conversion time.
- **Delete** — permanently removes a book: its EPUB(s) *and* its raw scan
  folder, if any. Confirmed via a browser dialog first, and blocked while a
  conversion job is queued or running against it. No undo. A single volume
  can also be deleted on its own, leaving the rest of the book (and its
  shared raw scan folder) intact.

### Server Status (admin only)

Settings → Server Status shows live host CPU/RAM/GPU metrics (utilization,
temperature, VRAM), sampled every 15s with the last 4 hours kept in memory
(resets on restart — this is live telemetry, not stored history). Useful for
watching GPU load/temperature during a long OCR batch.

---

## .env

```dotenv
POSTGRES_USER=yomekuro
POSTGRES_PASSWORD=change-me        # openssl rand -base64 24
POSTGRES_DB=yomekuro
YOMEKURO_ADMIN_USER=admin
YOMEKURO_ADMIN_PASSWORD=change-me
```

See `.env.example` for the full list, including optional tuning knobs
(`YOMEKURO_JOBS_POLL_INTERVAL_MS`, `YOMEKURO_ZIP_CACHE_SIZE`,
`CONVERTER_POLL_INTERVAL`, `CONVERTER_PROGRESS_EVERY`, `CONVERTER_MOKURO_RETRIES`,
`CONVERTER_MOKURO_RETRY_DELAY`) — all have sensible defaults if left unset.

---

## Build

```bash
# yomekuro
docker build -t yomekuro:latest .

# converter — CPU-only (~2.5GB)
docker build -f converter/Dockerfile.cpu -t converter:cpuonly converter/

# converter — AMD ROCm GPU (amd64 only, see Converter section)
docker build -f converter/Dockerfile.gpu -t converter:gpu converter/

# Go binary directly
CGO_ENABLED=0 go build -o yomekuro ./cmd/yomekuro
```

### Multi-arch (amd64 + arm64), push to registry

```bash
docker buildx create --name multi --driver docker-container --use   # once

docker buildx build --platform linux/amd64,linux/arm64 \
  -t truebad0ur/yomekuro:<tag> --push .

docker buildx build --platform linux/amd64,linux/arm64 \
  -f converter/Dockerfile.cpu -t truebad0ur/converter:cpuonly --push converter/
```

`Dockerfile.gpu` is amd64-only and tied to the host's GPU passthrough — build and
run it locally via `docker compose`, don't push it multi-arch.

### Releasing (CI)

`.github/workflows/release.yml` builds and pushes all three images to Docker
Hub automatically — but only when a tag shows up, and only if that tag points
at a commit on `main`. Ordinary commits, branches, and pull requests (including
from forks) never trigger a build. Either of these works:

```bash
# plain git tag + push
git checkout main
git tag <tag>
git push origin <tag>
```

or create a Release on GitHub (Releases → "Draft a new release" → type a new
tag name → Publish). Both fire the workflow — a tag pushed from the CLI is a
`push` event, a tag created via the Releases UI is a `release` event, and the
workflow listens for both.

Either way it pushes:

- `truebad0ur/yomekuro:<tag>` (linux/amd64 + linux/arm64)
- `truebad0ur/converter:cpu-<tag>` (linux/amd64)
- `truebad0ur/converter:gpu-<tag>` (linux/amd64)

The tag name itself becomes the image tag, whatever it is — there's no `v`
prefix convention enforced. Builds use GitHub Actions' layer cache
(`cache-from`/`cache-to: type=gha`) scoped per image, so re-running the
workflow (e.g. after a transient failure) doesn't rebuild layers that didn't
change. Before any image is built, a `test` job reruns `test.yml`
(gofmt/vet/build/test/golangci-lint for both modules) — a tag whose commit
fails that never gets published.

**Common release commands:**

```bash
# new commit, push, and tag it in one go
git add -A && git commit -m "msg" && git push origin main && git tag <tag> && git push origin <tag>

# tag whatever's already on main (no new commit)
git fetch origin main && git tag <tag> origin/main && git push origin <tag>

# amend the current commit, force-push main, move an existing tag onto it
git add . && git commit --amend --no-edit && git push origin main -f && git tag -f <tag> && git push origin <tag> -f
```

`git tag <name>` makes a lightweight tag — `git push origin <name>` (by name)
always pushes it, but `git push --follow-tags` silently skips lightweight tags
(it only follows annotated ones, `git tag -a`), so push tags by name explicitly.

**Bumping just one image manually:** the CI flow above always publishes all
three images under one shared tag, which is right for a coordinated release.
If only `yomekuro` changed (or only `converter`), there's no need to force a
matching version bump on the other side — build and push that one image by
hand under its own new tag, then point only its `.env` variable
(`YOMEKURO_VERSION` or `CONVERTER_VERSION`) at it:

```bash
docker build -t truebad0ur/yomekuro:<tag> .
docker push truebad0ur/yomekuro:<tag>
# .env: YOMEKURO_VERSION=<tag> (CONVERTER_VERSION stays wherever it was)
```

**Required secrets**, in the `prod` GitHub Environment (Settings → Environments
→ `prod` → Environment secrets — not repository-level secrets; the build jobs
declare `environment: prod` specifically to pick these up. Not needed on
forks, since forks don't inherit them and the workflow refuses to run outside
this repo anyway):

- `DOCKERHUB_USERNAME` — your Docker Hub username (`truebad0ur`).
- `DOCKERHUB_TOKEN` — a Docker Hub **access token**, not your account
  password: Docker Hub → Account Settings → Security → New Access Token,
  scope "Read & Write". Paste the token value as the secret.

---

## Converter (manga OCR → EPUB)

Uses [mokuro](https://github.com/kha-white/mokuro) for Japanese text detection.
`converter/docker-compose.yml` defines three services: `converter` (CPU,
one-shot CLI), `converter-gpu` (AMD ROCm, one-shot CLI), and `converter-worker`
(AMD ROCm, persistent — drains the upload queue below).

### Upload via UI (recommended)

Settings → Upload & Jobs: pick a library, a file, and a name. The file is
either an archive (`.zip`/`.tar`/`.tar.gz`/`.tar.xz`/`.7z`/`.rar`) of raw page
images, or a `.pdf`. yomekuro stages it into `<library>/<name>-in/`
(extracting archives and stripping OS junk — `.DS_Store`, `__MACOSX/`, `._*`
— along the way), and queues a row in Postgres (`conversion_jobs` table).
`converter-worker` picks it up and, per volume:

(A standalone `.html` file skips all of this — no OCR, no queue, it's just
copied straight into the library.)

- **Image pages / scanned PDF** (no text layer): runs mokuro OCR on GPU.
- **PDF with a real text layer**: skips OCR entirely — pulls the text and its
  exact position straight out of the PDF (`pdftotext -bbox-layout`) and lays
  it over the rendered page images, same fixed-layout shape as an OCR'd
  volume. Whether a PDF counts as "has a text layer" is decided automatically
  (average non-whitespace characters per page above a threshold).

Either way the result is EPUBs written to `<library>/<name>/`, picked up by
the next library scan automatically. Job status is polled in the same
Settings page.

This needs `./library` mounted read-write (it is, by default) — staging
writes into it.

### Manual folders

Dropping a pre-staged `<name>-in/` folder into the library by hand (no upload)
also works — `converter-worker` polls for these too and converts them the same
way, skipping ones already fully converted. Useful for content prepared some
other way, or moved in from elsewhere.

### CLI (manual one-shot runs)

For ad-hoc runs outside the upload flow.

#### Input layout

One subfolder per volume (each becomes its own EPUB):

```
library/manga/test-in/
  Dungeon Meshi v01/
    001.jpg
    002.jpg
  Dungeon Meshi v02/
    ...
```

Or point `--input` straight at a folder of images with no subfolders — it's
treated as a single volume/EPUB, named after the folder:

```
library/manga/One-Shot Story-in/
  001.jpg
  002.jpg
```

#### Run

```bash
# all volumes (CPU)
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/manga/test-in --output /library/manga/test

# same, on GPU
docker compose -f converter/docker-compose.yml run --rm converter-gpu \
  --input /library/manga/test-in --output /library/manga/test

# single volume, force re-run
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/manga/test-in --output /library/manga/test \
  --volume "Dungeon Meshi v01" --no-cache
```

Model weights download on first run, cached in `converter/data/`.

### OCR quality vs. speed

The text detector runs at double mokuro's own default resolution, trading
roughly 2-3x more time per page for meaningfully fewer misreads — a small
numbered-list marker or dense footer text is much less likely to get merged
into a neighboring line's OCR pass. Applies to every future conversion;
already-converted books are unaffected until reconverted (Settings →
Converting → Reconvert).

### AMD GPU (ROCm)

PyTorch's ROCm wheel bundles its own runtime libs — no ROCm apt packages needed
in the image, just a host with the `amdgpu` kernel driver and `/dev/kfd`/`/dev/dri`.
`converter-gpu` already passes those through plus `HSA_OVERRIDE_GFX_VERSION=10.3.0`,
needed because most RDNA2 GPUs below the top tier (Navi 21/22/23 — RX
6700/6700S/6650XT/6600 etc.) report a `gfx103x` ID that ROCm doesn't ship
optimized kernels for; overriding to `10.3.0` (gfx1030, RX 6800/6900, same
generation) works in practice. Don't override across RDNA generations.

`group_add` GIDs (`44`/`992`) are this host's `video`/`render` groups
(`getent group video render`) — check they match elsewhere.

---

## Reader

- Fixed-layout manga: page-by-page, RTL support, Yomitan works on OCR text without iframe
- Novels: scrolling or vertical (RTL) layout
- Keyboard: `←` / `→` — prev/next page; `↑` / `↓` — scroll within zoomed page; `Ctrl +` / `Ctrl -` / `Ctrl 0` — zoom in/out/reset
- Touch: swipe or tap the left/right edge to turn manga pages; long-press selects text for a bookmark, a bare tap never does
- Spread view: toggle **Spread** button in the nav bar

### Text lookup (Yomitan / 10ten)

On manga and PDF pages the OCR'd (or PDF-extracted) text is an invisible,
selectable layer laid directly over the printed characters, so pop-up
dictionaries — [Yomitan](https://github.com/themoeway/yomitan), 10ten Japanese
Reader — look words up on hover with no white text box getting in the way
(unlike mokuro's own reader, which reveals the recognized text on hover).

Each line stays a single element with one continuous text run, so dictionaries
can still assemble multi-character words across it. Landing that layer *on* the
glyphs takes some care: a detector box wraps a line's ink rather than its
character cells, leans with the print, and is looser than the glyphs it holds —
all of which the converter measures out (`ocrSpanSlack` / `lineGeometry` in
`converter/epub.go`) so the overlay tracks the page instead of drifting off it.
PDF text-layer pages skip all this and use the PDF's own coordinates.

---

## Libraries

`docker-compose.yml` mounts a single volume:

```yaml
volumes:
  - ./library:/library
```

Inside it, two subfolders are each auto-registered as their own library and
scanned on boot — no manual "add library" step needed:

```
library/
  manga/    # manga EPUBs (converter output or your own), one folder per series
  books/    # everything else: light novels, PDFs, standalone .html files —
            # one folder per series/book, .html files sit directly inside
```

The whole `./library` mount is read-write (not `:ro`) because the upload
feature extracts archives directly into whichever library you pick
(`library/manga/` or `library/books/`).

HTML book titles come from `<title>`, with optional
`<meta name="author" content="...">` and
`<meta name="reading-direction" content="rtl">` in the `<head>`. Since a
standalone HTML file has no embedded cover the way EPUB does, its
library-page thumbnail is generated automatically — title plus a short
excerpt of the body text on a plain card.

---

## License

ISC — see [LICENSE](LICENSE).
