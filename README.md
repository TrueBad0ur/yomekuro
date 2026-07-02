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

# converter — GPU/CUDA (~9.5GB, for amd64 with NVIDIA GPU)
docker build -f converter/Dockerfile.gpucpu -t converter:gpucpu converter/
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

# converter — GPU/CUDA (arm64 gets CPU automatically, no CUDA on arm64)
docker buildx build --platform linux/amd64,linux/arm64 \
  -f converter/Dockerfile.gpucpu \
  -t truebad0ur/converter:gpucpu --push converter/
```

### Go binary directly

```bash
CGO_ENABLED=0 go build -o yomekuro ./cmd/yomekuro
```

---

## Converter (manga OCR → EPUB)

Converts a folder of manga images into a fixed-layout EPUB with transparent OCR text overlay. Uses [mokuro](https://github.com/kha-white/mokuro) for Japanese text detection.

### Docker images

| Dockerfile | Tag | Size | When to use |
|------------|-----|------|-------------|
| `Dockerfile.cpu` | `cpuonly` | ~2.5GB | Home server, NAS, any machine without NVIDIA GPU |
| `Dockerfile.gpucpu` | `gpucpu` | ~9.5GB | amd64 with NVIDIA GPU (faster OCR) |

`docker-compose.yml` uses `Dockerfile.cpu` by default.

### Input layout

```
library/test/
  Dungeon Meshi v01/   ← one subfolder = one volume
    001.jpg
    002.jpg
    ...
  Dungeon Meshi v02/
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
