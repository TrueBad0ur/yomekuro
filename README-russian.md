# yomekuro

[English](README.md)

Самостоятельно размещаемая (self-hosted) EPUB-библиотека для японских ранобэ и манги. Один бинарник + PostgreSQL. Никакого OAuth, никаких внешних источников метаданных — всё берётся напрямую из EPUB-файлов.

Включает в себя отдельный **конвертер**, превращающий папки с картинками манги в EPUB с фиксированной вёрсткой и OCR-слоем текста для [Yomitan](https://github.com/themoeway/yomitan).

---

## Быстрый старт

```bash
cp .env.example .env
# отредактируйте .env — задайте POSTGRES_PASSWORD и YOMEKURO_ADMIN_PASSWORD

docker compose up -d --build
```

`docker-compose.yml` — это симлинк на `docker-compose.dev.yml`: он собирает
все образы из исходников (yomekuro + сервисы конвертера) прямо на вашей
машине. Для продакшн-хоста, на котором не нужен Go/Python-тулчейн и просто
запускается уже выпущенная версия, используйте вместо этого
`docker-compose.prod.yml` — он тянет готовые образы с Docker Hub:

```bash
cp .env.example .env
# отредактируйте .env — задайте POSTGRES_PASSWORD, YOMEKURO_ADMIN_PASSWORD
# и два тега образов для скачивания: YOMEKURO_VERSION и CONVERTER_VERSION

docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
```

`yomekuro` и `converter` версионируются и публикуются независимо друг от
друга (`YOMEKURO_VERSION` выбирает `truebad0ur/yomekuro:<tag>`,
`CONVERTER_VERSION` — `truebad0ur/converter:gpu-<tag>`) — можно поднять
версию одного без пересборки/скачивания другого, если менялась только одна
сторона. См. раздел "Релизы" ниже о том, как версии публикуются на Docker
Hub.

Монтируется один каталог `./library` с тремя подпапками внутри: `ranobe/`,
`manga/` и `html/` (в первых двух — одна папка на серию, внутри `.epub`
файлы; `html/` хранит отдельные `.html` файлы, один файл = одна книга). Все
три регистрируются как отдельные библиотеки и сканируются автоматически при
старте. Откройте http://localhost:8080 и войдите.

Dev-компоуз также поднимает сервисы конвертера (`converter`,
`converter-gpu`, `converter-worker` — см. "Конвертер" ниже) через `include:`
в `docker-compose.dev.yml`. `converter/docker-compose.yml` по-прежнему
работает отдельно, если нужен только конвертер без yomekuro.

---

## Использование yomekuro

### Главная страница библиотеки

Главная страница показывает ваши серии в виде плиток с обложками,
сгруппированных по библиотекам (Ranobe / Manga / HTML) в боковой панели.
Клик по серии показывает её книги, клик по книге открывает чтение. Поиск и
фильтры по жанрам/тегам — в шапке; у администраторов на каждой книге есть
дополнительная кнопка редактирования тегов.

![Главная страница библиотеки](docs/screenshots/library.png)

### Чтение

Манга открывается в режиме постраничного просмотра с фиксированной вёрсткой
(с переключателем **Spread** для разворотов на два листа); ранобэ
открываются в прокручиваемом или вертикальном (RTL) режиме. Yomitan работает
напрямую с OCR-текстом поверх страниц манги — без iframe. Клавиатурные
сочетания — см. раздел [Читалка](#читалка) ниже.

![Читалка](docs/screenshots/reader.png)

### Закладки

Выделите текст во время чтения, чтобы отметить его закладкой (подсветкой);
выделения сохраняются отдельно для каждой книги и не ломаются на страницах
с фуриганой (`<ruby>`/`<rt>`), так как в тег оборачиваются только отдельные
текстовые узлы, а не целые элементы.

![Закладки](docs/screenshots/bookmarks.png)

### Настройки (только для администраторов)

У обычных пользователей в шапке есть только переключатель темы и кнопка
выхода. У администраторов дополнительно есть страница настроек — управление
библиотеками, пользователями и загрузка манги на OCR-конвертацию.

![Страница настроек](docs/screenshots/settings.png)

### Загрузка манги на OCR (только для администраторов)

Настройки → Upload manga: выберите библиотеку, архив с исходными
изображениями страниц и имя. Задача становится в очередь, и её прогресс
(текущий том, число страниц) стримится в лог прямо на той же странице, пока
EPUB не будет готов и не появится в библиотеке.

![Лог загрузки/конвертации](docs/screenshots/conversion-log.png)

---

## .env

```dotenv
POSTGRES_USER=yomekuro
POSTGRES_PASSWORD=change-me        # openssl rand -base64 24
POSTGRES_DB=yomekuro
YOMEKURO_ADMIN_USER=admin
YOMEKURO_ADMIN_PASSWORD=change-me
```

Полный список — в `.env.example`, включая опциональные настройки
(`YOMEKURO_JOBS_POLL_INTERVAL_MS`, `YOMEKURO_ZIP_CACHE_SIZE`,
`CONVERTER_POLL_INTERVAL`, `CONVERTER_PROGRESS_EVERY`,
`CONVERTER_MOKURO_RETRIES`, `CONVERTER_MOKURO_RETRY_DELAY`) — у всех есть
разумные значения по умолчанию, если не заданы.

---

## Сборка

```bash
# yomekuro
docker build -t yomekuro:latest .

# converter — только CPU (~2.5ГБ)
docker build -f converter/Dockerfile.cpu -t converter:cpuonly converter/

# converter — AMD ROCm GPU (только amd64, см. раздел "Конвертер")
docker build -f converter/Dockerfile.gpu -t converter:gpu converter/

# Go-бинарник напрямую
CGO_ENABLED=0 go build -o yomekuro ./cmd/yomekuro
```

### Мульти-архитектурная сборка (amd64 + arm64), пуш в реестр

```bash
docker buildx create --name multi --driver docker-container --use   # один раз

docker buildx build --platform linux/amd64,linux/arm64 \
  -t truebad0ur/yomekuro:<tag> --push .

docker buildx build --platform linux/amd64,linux/arm64 \
  -f converter/Dockerfile.cpu -t truebad0ur/converter:cpuonly --push converter/
```

`Dockerfile.gpu` собирается только под amd64 и завязан на проброс GPU
хоста — собирайте и запускайте его локально через `docker compose`, не
пушьте мульти-архитектурно.

### Релизы (CI)

`.github/workflows/release.yml` автоматически собирает и пушит все три
образа на Docker Hub — но только когда появляется тег, и только если этот
тег указывает на коммит в `main`. Обычные коммиты, ветки и pull request'ы
(в том числе из форков) сборку никогда не запускают. Работает любой из
двух способов:

```bash
# обычный git tag + push
git checkout main
git tag <tag>
git push origin <tag>
```

либо создать Release на GitHub (Releases → "Draft a new release" → указать
новый тег → Publish). Оба способа запускают workflow — тег, запушенный из
CLI, это событие `push`, тег, созданный через интерфейс Releases — событие
`release`, и workflow слушает оба.

В любом случае будет запушено:

- `truebad0ur/yomekuro:<tag>` (linux/amd64 + linux/arm64)
- `truebad0ur/converter:cpu-<tag>` (linux/amd64)
- `truebad0ur/converter:gpu-<tag>` (linux/amd64)

Само имя тега становится тегом образа как есть — никакого принудительного
префикса `v`. Сборки используют кэш слоёв GitHub Actions
(`cache-from`/`cache-to: type=gha`), отдельный для каждого образа, поэтому
повторный запуск workflow (например, после случайного сбоя) не пересобирает
неизменившиеся слои. Перед сборкой любого образа джоба `test` заново
запускает `test.yml` (gofmt/vet/build/test/golangci-lint для обоих
модулей) — если коммит под тегом его не проходит, публикации не будет.

**Частые команды для релизов:**

```bash
# новый коммит, пуш и тег одной командой
git add -A && git commit -m "msg" && git push origin main && git tag <tag> && git push origin <tag>

# затегать то, что уже есть в main (без нового коммита)
git fetch origin main && git tag <tag> origin/main && git push origin <tag>

# amend текущего коммита, force-push main, перенос существующего тега на него
git add . && git commit --amend --no-edit && git push origin main -f && git tag -f <tag> && git push origin <tag> -f
```

`git tag <name>` создаёт легковесный (lightweight) тег — `git push origin
<name>` (по имени) всегда его пушит, а вот `git push --follow-tags`
молча пропускает легковесные теги (следует только за аннотированными,
`git tag -a`), поэтому теги нужно пушить явно по имени.

**Обновление только одного образа вручную:** описанный выше CI-флоу всегда
публикует все три образа под одним общим тегом, что правильно для
скоординированного релиза. Если менялся только `yomekuro` (или только
`converter`), необязательно принудительно поднимать версию и на другой
стороне — соберите и запушьте нужный образ вручную под своим новым тегом,
затем поменяйте в `.env` только соответствующую переменную
(`YOMEKURO_VERSION` или `CONVERTER_VERSION`):

```bash
docker build -t truebad0ur/yomekuro:<tag> .
docker push truebad0ur/yomekuro:<tag>
# .env: YOMEKURO_VERSION=<tag> (CONVERTER_VERSION остаётся как был)
```

**Необходимые секреты** в GitHub Environment `prod` (Settings → Environments
→ `prod` → Environment secrets — не секреты уровня репозитория; джобы сборки
явно указывают `environment: prod`, чтобы их получить. Форкам не нужны,
так как форки их не наследуют, а workflow вообще отказывается запускаться
за пределами этого репозитория):

- `DOCKERHUB_USERNAME` — ваш логин на Docker Hub (`truebad0ur`).
- `DOCKERHUB_TOKEN` — **access-токен** Docker Hub, а не пароль от
  аккаунта: Docker Hub → Account Settings → Security → New Access Token,
  scope "Read & Write". Вставьте значение токена в качестве секрета.

---

## Конвертер (манга-OCR → EPUB)

Использует [mokuro](https://github.com/kha-white/mokuro) для распознавания
японского текста. `converter/docker-compose.yml` определяет три сервиса:
`converter` (CPU, одноразовый CLI), `converter-gpu` (AMD ROCm, одноразовый
CLI) и `converter-worker` (AMD ROCm, постоянно работающий — разбирает
очередь загрузок, см. ниже).

### Загрузка через UI (рекомендуется)

Settings → Upload manga: выберите библиотеку, архив (`.zip`/`.tar`/
`.tar.gz`/`.tar.xz`/`.7z`/`.rar`) с исходными изображениями страниц и имя.
yomekuro распаковывает его в `<library>/<name>-in/`, убирает системный мусор
(`.DS_Store`, `__MACOSX/`, `._*` — типичны для архивов, сделанных на macOS)
и ставит строку в очередь в Postgres (таблица `conversion_jobs`).
`converter-worker` забирает её, запускает OCR на GPU и записывает EPUB в
`<library>/<name>/` — их подхватит следующее сканирование библиотеки
автоматически. Статус задачи опрашивается на той же странице настроек.

Для этого нужен `./library`, смонтированный на чтение-запись (по умолчанию
так и есть) — на этап извлечения архива требуется запись.

### Папки вручную

Подкладывание заранее подготовленной папки `<name>-in/` в библиотеку вручную
(без загрузки через UI) тоже работает — `converter-worker` опрашивает и их,
конвертируя так же, пропуская уже полностью сконвертированные. Полезно для
контента, подготовленного каким-то другим способом или перенесённого
откуда-то ещё.

### CLI (ручные одноразовые запуски)

Для разовых запусков вне общего флоу загрузки.

#### Структура входных данных

Одна подпапка на том (каждая станет своим EPUB):

```
library/manga/test-in/
  Dungeon Meshi v01/
    001.jpg
    002.jpg
  Dungeon Meshi v02/
    ...
```

Либо укажите `--input` прямо на папку с изображениями без подпапок — она
будет обработана как один том/EPUB, названный по имени папки:

```
library/manga/One-Shot Story-in/
  001.jpg
  002.jpg
```

#### Запуск

```bash
# все тома (CPU)
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/manga/test-in --output /library/manga/test

# то же самое, на GPU
docker compose -f converter/docker-compose.yml run --rm converter-gpu \
  --input /library/manga/test-in --output /library/manga/test

# один том, принудительный перезапуск
docker compose -f converter/docker-compose.yml run --rm converter \
  --input /library/manga/test-in --output /library/manga/test \
  --volume "Dungeon Meshi v01" --no-cache
```

Веса модели скачиваются при первом запуске и кэшируются в `converter/data/`.

### AMD GPU (ROCm)

ROCm-сборка PyTorch несёт свои собственные библиотеки рантайма — никакие
apt-пакеты ROCm в образе не нужны, нужен только хост с драйвером ядра
`amdgpu` и `/dev/kfd`/`/dev/dri`. `converter-gpu` уже пробрасывает их, плюс
`HSA_OVERRIDE_GFX_VERSION=10.3.0` — нужно потому, что большинство GPU RDNA2
не топовых моделей (Navi 21/22/23 — RX 6700/6700S/6650XT/6600 и т.д.)
сообщают ID `gfx103x`, для которого ROCm не поставляет оптимизированные
ядра; переопределение на `10.3.0` (gfx1030, RX 6800/6900, то же поколение)
на практике работает. Не переопределяйте между разными поколениями RDNA.

GID-ы в `group_add` (`44`/`992`) — это группы `video`/`render` конкретно
этого хоста (`getent group video render`) — проверьте, что они совпадают
на своём.

---

## Читалка

- Манга с фиксированной вёрсткой: постранично, поддержка RTL, Yomitan
  работает с OCR-текстом без iframe
- Ранобэ: прокручиваемый или вертикальный (RTL) режим
- Клавиатура: `←` / `→` — предыдущая/следующая страница; `↑` / `↓` —
  прокрутка внутри увеличенной страницы; `Ctrl +` / `Ctrl -` / `Ctrl 0` —
  увеличение/уменьшение/сброс масштаба
- Режим разворота: переключатель **Spread** в панели навигации

---

## Библиотеки

`docker-compose.yml` монтирует один том:

```yaml
volumes:
  - ./library:/library
```

Внутри него три подпапки автоматически регистрируются как отдельные
библиотеки и сканируются при старте — вручную ничего добавлять не нужно:

```
library/
  ranobe/   # EPUB ранобэ, одна папка на серию
  manga/    # EPUB манги (результат конвертера или своя), одна папка на серию
  html/     # отдельные .html файлы, один файл = одна книга
```

Весь том `./library` монтируется на чтение-запись (не `:ro`), потому что
функция загрузки манги распаковывает архивы прямо в `library/manga/`.

Заголовки HTML-книг берутся из `<title>`, с опциональными
`<meta name="author" content="...">` и
`<meta name="reading-direction" content="rtl">` в `<head>`.

---

## Лицензия

ISC — см. [LICENSE](LICENSE).
