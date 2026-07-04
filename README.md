# yomekuro

Self-hosted EPUB library for Japanese light novels and manga. Single binary + PostgreSQL. No OAuth, no external metadata providers — everything from EPUB files directly.

Includes a companion **converter** that turns manga image folders into fixed-layout EPUBs with OCR text overlay for [Yomitan](https://github.com/themoeway/yomitan).

---

## Quick start

```bash
cp .env.example .env
# edit .env — set POSTGRES_PASSWORD and YOMEKURO_ADMIN_PASSWORD

docker compose up -d
```

Mounts `./library` (EPUB/manga) and `./html-library` (standalone `.html` files) —
both are registered and scanned automatically on boot. Open http://localhost:8080
and log in.

This also brings up the converter services (`converter`, `converter-gpu`,
`converter-worker` — see "Converter" below) via `docker-compose.yml`'s
`include:`. `converter/docker-compose.yml` still works standalone if you only
want the converter without yomekuro.

---

## .env

```dotenv
POSTGRES_USER=yomekuro
POSTGRES_PASSWORD=change-me        # openssl rand -base64 24
POSTGRES_DB=yomekuro
YOMEKURO_ADMIN_USER=admin
YOMEKURO_ADMIN_PASSWORD=change-me
```

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
  -t truebad0ur/yomekuro:0.2 --push .

docker buildx build --platform linux/amd64,linux/arm64 \
  -f converter/Dockerfile.cpu -t truebad0ur/converter:cpuonly --push converter/
```

`Dockerfile.gpu` is amd64-only and tied to the host's GPU passthrough — build and
run it locally via `docker compose`, don't push it multi-arch.

---

## Converter (manga OCR → EPUB)

Uses [mokuro](https://github.com/kha-white/mokuro) for Japanese text detection.
`converter/docker-compose.yml` defines three services: `converter` (CPU,
one-shot CLI), `converter-gpu` (AMD ROCm, one-shot CLI), and `converter-worker`
(AMD ROCm, persistent — drains the upload queue below).

### Upload via UI (recommended)

Settings → Upload manga: pick a library, an archive (`.zip`/`.tar`/`.tar.gz`/
`.tar.xz`/`.7z`) of raw page images, and a name. yomekuro extracts it into
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
library/test/
  Dungeon Meshi v01/
    001.jpg
    002.jpg
  Dungeon Meshi v02/
    ...
```

Or point `--input` straight at a folder of images with no subfolders — it's
treated as a single volume/EPUB, named after the folder:

```
library/One-Shot Story/
  001.jpg
  002.jpg
```

#### Run

```bash
# all volumes (CPU)
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/test --output /library/Manga

# same, on GPU
docker compose -f converter/docker-compose.yml run --rm converter-gpu \
  --input /library/test --output /library/Manga

# single volume, force re-run
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/test --output /library/Manga \
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

`docker-compose.yml` mounts two volumes, both auto-registered and scanned on
boot — no manual "add library" step needed:

```yaml
volumes:
  - ./library:/library               # EPUB / manga, one folder per series
  - ./html-library:/html-library:ro  # standalone .html files, one file = one book
```

`./library` is read-write (not `:ro`) because the manga upload feature
extracts archives into it directly; `./html-library` stays read-only since
nothing writes to it.

HTML book titles come from `<title>`, with optional
`<meta name="author" content="...">` and
`<meta name="reading-direction" content="rtl">` in the `<head>`.

---

## License

ISC — see [LICENSE](LICENSE).
