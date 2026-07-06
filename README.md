# yomekuro

[Русский](README-russian.md)

Self-hosted EPUB library for Japanese light novels and manga. Single binary + PostgreSQL. No OAuth, no external metadata providers — everything from EPUB files directly.

Includes a companion **converter** that turns manga image folders into fixed-layout EPUBs with OCR text overlay for [Yomitan](https://github.com/themoeway/yomitan).

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

Mounts a single `./library` — with three subfolders inside, `ranobe/`, `manga/`,
and `html/` (one series per subfolder of those, `.epub` files inside; `html/`
holds standalone `.html` files, one file = one book). All three are registered
as separate libraries and scanned automatically on boot. Open
http://localhost:8080 and log in.

The dev compose also brings up the converter services (`converter`,
`converter-gpu`, `converter-worker` — see "Converter" below) via
`docker-compose.dev.yml`'s `include:`. `converter/docker-compose.yml` still
works standalone if you only want the converter without yomekuro.

---

## Using yomekuro

### Library page

The home page lists your series as cover-art tiles, grouped by library
(Ranobe / Manga / HTML) in the sidebar. Click a series to see its books,
click a book to start reading. Search and genre/tag filters are in the
header; admins get an extra button on each book to edit its tags.

![Library page](docs/screenshots/library.png)

### Reading

Manga opens in fixed-layout page view (with a **Spread** toggle for
two-page spreads); novels open in a scrolling or vertical (RTL) layout.
Yomitan works directly against the OCR text overlay on manga pages — no
iframe getting in the way. See [Reader](#reader) below for keyboard
shortcuts.

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

### Uploading manga for OCR (admin only)

Settings → Upload manga: pick a library, an archive of raw page images, and
a name. The job is queued and its progress (current volume, page count)
streams into a live log on the same page until the EPUB is ready and shows
up in the library.

![Upload/conversion log](docs/screenshots/conversion-log.png)

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

Settings → Upload manga: pick a library, an archive (`.zip`/`.tar`/`.tar.gz`/
`.tar.xz`/`.7z`/`.rar`) of raw page images, and a name. yomekuro extracts it into
`<library>/<name>-in/`, strips OS junk (`.DS_Store`, `__MACOSX/`, `._*` — common
in macOS-made archives), and queues a row in Postgres (`conversion_jobs`
table). `converter-worker` picks it up, runs OCR on GPU, and writes EPUBs to
`<library>/<name>/` — picked up by the next library scan automatically. Job
status is polled in the same Settings page.

This needs `./library` mounted read-write (it is, by default) — the extraction
step writes into it.

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
- Spread view: toggle **Spread** button in the nav bar

---

## Libraries

`docker-compose.yml` mounts a single volume:

```yaml
volumes:
  - ./library:/library
```

Inside it, three subfolders are each auto-registered as their own library and
scanned on boot — no manual "add library" step needed:

```
library/
  ranobe/   # light novel EPUBs, one folder per series
  manga/    # manga EPUBs (converter output or your own), one folder per series
  html/     # standalone .html files, one file = one book
```

The whole `./library` mount is read-write (not `:ro`) because the manga
upload feature extracts archives directly into `library/manga/`.

HTML book titles come from `<title>`, with optional
`<meta name="author" content="...">` and
`<meta name="reading-direction" content="rtl">` in the `<head>`.

---

## License

ISC — see [LICENSE](LICENSE).
