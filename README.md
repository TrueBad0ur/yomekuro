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

Open http://localhost:8080, log in, add a library pointing to your `/library` path, scan.

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

### Local (current arch)

```bash
# yomekuro
docker build -t yomekuro:latest .

# converter — CPU-only (recommended, ~2.5GB)
docker build -f converter/Dockerfile.cpu -t converter:cpuonly converter/

# converter — AMD ROCm GPU (amd64 only, needs a ROCm-capable host — see below)
docker build -f converter/Dockerfile.gpu -t converter:gpu converter/
```

### Multi-arch (amd64 + arm64), push to registry

```bash
# create builder once
docker buildx create --name multi --driver docker-container --use

# yomekuro
docker buildx build --platform linux/amd64,linux/arm64 \
  -t truebad0ur/yomekuro:0.2 --push .

# converter — CPU-only
docker buildx build --platform linux/amd64,linux/arm64 \
  -f converter/Dockerfile.cpu \
  -t truebad0ur/converter:cpuonly --push converter/
```

`Dockerfile.gpu` (AMD ROCm) is amd64-only and tied to the host's GPU passthrough
setup (see below) — build and run it locally with `docker compose`, don't push it
as a multi-arch image.

### Go binary directly

```bash
CGO_ENABLED=0 go build -o yomekuro ./cmd/yomekuro
```

---

## Converter (manga OCR → EPUB)

Converts a folder of manga images into a fixed-layout EPUB with transparent OCR text overlay. Uses [mokuro](https://github.com/kha-white/mokuro) for Japanese text detection.

### Docker images

| Dockerfile | Tag | When to use |
|------------|-----|-------------|
| `Dockerfile.cpu` | `cpuonly` | Home server, NAS, any machine without a GPU |
| `Dockerfile.gpu` | `gpu` | amd64 with an AMD ROCm-capable GPU (faster OCR). No NVIDIA/CUDA support. |

`docker-compose.yml` uses `Dockerfile.cpu` (service `converter`) by default; the GPU
build is service `converter-gpu`.

### AMD GPU (ROCm)

PyTorch's official ROCm wheels bundle their own runtime libs, so the image itself needs
no system ROCm packages — only a host with the `amdgpu` kernel driver and
`/dev/kfd`/`/dev/dri` exposed. `docker-compose.yml`'s `converter-gpu` service already
passes those through plus `HSA_OVERRIDE_GFX_VERSION=10.3.0`, needed because most
RDNA2 GPUs below the top tier (e.g. Navi 21/22/23 — RX 6700/6700S/6650XT/6600 etc.)
report a `gfx103x` ID that ROCm doesn't officially ship optimized kernels for; overriding
to `10.3.0` (gfx1030, RX 6800/6900 — same generation, officially supported) works in
practice. Don't override across RDNA generations (e.g. RDNA2 → RDNA3 ids).

The `group_add` GIDs (`44`/`992`) are this host's `video`/`render` group IDs
(`getent group video render`) — check they match on a different machine.

```bash
docker compose -f converter/docker-compose.yml run --rm converter-gpu \
  --input /library/test --output /library/Manga
```

### Input layout

Point `--input` at a folder of volume subfolders (one subfolder = one volume,
each becomes its own EPUB):

```
library/test/
  Dungeon Meshi v01/   ← one subfolder = one volume
    001.jpg
    002.jpg
    ...
  Dungeon Meshi v02/
    ...
```

Or point it directly at a folder containing page images with no subfolders —
that folder itself is treated as a single volume, producing one EPUB named
after it:

```
library/One-Shot Story/
  001.jpg
  002.jpg
  ...
```

### Run

```bash
# all volumes
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/test --output /library/Manga

# single volume (re-runs OCR, clears cache)
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/test --output /library/Manga \
  --volume "Dungeon Meshi v01" --no-cache
```

HuggingFace model is downloaded on first run and cached in `converter/data/hf-cache/`.

---

## Reader

- Fixed-layout manga: page-by-page, RTL support, Yomitan works on OCR text without iframe
- Novels: scrolling or vertical (RTL) layout
- Keyboard: `←` / `→` — prev/next page; `↑` / `↓` — scroll within zoomed page; `Ctrl +` / `Ctrl -` / `Ctrl 0` — zoom in/out/reset
- Spread view: toggle **Spread** button in the nav bar

---

## Library path

Mount your EPUB folder into `/library` in docker-compose.yml:

```yaml
volumes:
  - /path/to/your/epubs:/library:ro
```

Then add the library at http://localhost:8080 with path `/library`.

### HTML library

`docker-compose.yml` also mounts `./html-library:/html-library:ro`. Drop standalone
`.html` files there — each file is treated as a one-page book. Title comes from
`<title>`, with optional `<meta name="author" content="...">` and
`<meta name="reading-direction" content="rtl">` in the `<head>`. Add the library
at http://localhost:8080 with path `/html-library`.

---

## License

ISC — see [LICENSE](LICENSE).
