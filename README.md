# IPFS File Storage

HTTP-сервис для децентрализованного хранения файлов поверх [IPFS](https://ipfs.tech/) (Kubo) с поддержкой кластерной репликации, адаптивного видео-стриминга и мягкого удаления.

## Архитектура

```
                    ┌──────────────────────────────────────────────┐
                    │              Docker Network                  │
                    │           172.20.0.0/24                      │
                    │                                              │
┌──────────┐       │  ┌─────────────────────────────────────┐    │
│  Client   │       │  │   ipfs-bootstrap  (172.20.0.10)     │    │
│           │       │  │   DHT-сервер, peer discovery        │    │
└─────┬─────┘       │  │   Нет пользовательских данных      │    │
      │ HTTP        │  └──────────────┬──────────────────────┘    │
      ▼             │                 │ swarm connect              │
┌──────────┐       │       ┌─────────┴──────────┐                │
│   nginx   │       │       │                    │                │
│  :8081    │       │  ┌────▼─────┐      ┌──────▼──────┐         │
│ round-    │───────▶  │  ipfs1    │      │   ipfs2     │         │
│ robin     │       │  │ :5001    │◄────▶│  :5001      │         │
└──────────┘       │  │ swarm    │ bitswap│  swarm     │         │
                    │  └────▲─────┘      └──────▲──────┘         │
                    │       │                    │                │
                    │  ┌────┴─────┐      ┌──────┴──────┐         │
                    │  │storage1  │      │  storage2   │         │
                    │  │ :3000    │      │  :3000      │         │
                    │  │ Go API   │      │  Go API    │         │
                    │  │ +ffmpeg  │      │  +ffmpeg    │         │
                    │  └──────────┘      └────────────┘         │
                    │                                              │
                    └──────────────────────────────────────────────┘

Порты на хосте:
  nginx     → :${NGINX_PORT:-8081}      (балансировщик)
  storage1  → :${STORAGE1_PORT:-3001}   (API напрямую)
  storage2  → :${STORAGE2_PORT:-3002}   (API напрямую)
```

### Компоненты

| Компонент | Образ | Назначение |
|-----------|-------|------------|
| `ipfs-bootstrap` | `ipfs/kubo:latest` | Лёгкая нода для peer discovery. DHT-сервер, не хранит пользовательские данные |
| `ipfs1` | `ipfs/kubo:latest` | Хранилище 1. Full DHT (Routing.Type=dht), хранит и раздаёт блоки |
| `ipfs2` | `ipfs/kubo:latest` | Хранилище 2. Full DHT, хранит и раздаёт блоки |
| `storage1` | Go + ffmpeg (сборка из Dockerfile) | HTTP API, привязан к ipfs1 |
| `storage2` | Go + ffmpeg (сборка из Dockerfile) | HTTP API, привязан к ipfs2 |
| `nginx` | `nginx:alpine` | Round-robin балансировщик на :8081 |

### Приватный swarm

Все IPFS-ноды работают в приватной сети через общий `swarm.key`. Ноды не подключаются к публичному IPFS — все bootstrap-пиры удалены, AutoConf.Enabled=false (обязательно для Kubo 0.41+).

### Репликация

```
POST /upload → storage1 → ClusterAdd(payload)
                              ├→ ipfs1.Add() ─┐
                              └→ ipfs2.Add() ─┴→ одинаковый CID → 200 response
                                                    ↓
                                      async ClusterReplicate(CID):
                                        ├→ ipfs1: verify + Pin
                                        └→ ipfs2: verify + Pin
```

`ClusterAdd()` и `ClusterAddDir()` напрямую записывают данные на каждую настроенную ноду и проверяют, что все ноды вернули одинаковый CID. Поэтому новая загрузка не зависит от задержек provider discovery или Bitswap. После записи `ClusterReplicate()` проверяет доступность и фиксирует CID через recursive pin. Для обычных upload-эндпоинтов pin запускается асинхронно, чтобы клиент сразу получил CID; видео дожидается pin всех связанных CID.

### Мягкое удаление

1. `DELETE /file/{cid}` — CID добавляется в unpin-store с timestamp
2. `GET /file/{cid}` — проверяет unpin-store, возвращает 404 если удалён
3. Фоновый GC-воркер — каждые `UNPIN_GC_INTERVAL` проверяет истёкшие записи
4. Физический unpin — после `UNPIN_TTL` файл анпиннится на всех нодах

Каждый storage-сервис имеет свой отдельный Docker volume для unpin-store (`unpin-data-1`, `unpin-data-2`), чтобы избежать race condition при параллельной записи.

### Видео-стриминг

Загруженное видео проходит через ffmpeg и разбивается на HLS/CMAF-чанки с тремя уровнями качества:

```
POST /upload-video (video.mp4)
  ├→ validate: ffprobe — размер, длительность ≤60с, пропорции 9:16
  ├→ transcode: ffmpeg — poster JPEG + 3 варианта (low/medium/high), fMP4 CMAF
  ├→ privacy posters: blur + blur_faces локальным face detector
  ├→ upload: каждый чанк → IPFS → CID
  ├→ rewrite: плейлисты переписываются — ссылки на CID
  ├→ replicate: все CID реплицируются на все ноды
  └→ AddGroup: masterCID → [all CIDs] для группового удаления
```

Клиент запрашивает мастер-плейлист, браузер автоматически выбирает качество по пропускной способности:

```
GET /stream/{masterCID}/master.m3u8
  → #EXTM3U
  → #EXT-X-STREAM-INF:BANDWIDTH=500000
  → /stream/segment/QmLow.m3u8
  → #EXT-X-STREAM-INF:BANDWIDTH=1500000
  → /stream/segment/QmMed.m3u8
  → #EXT-X-STREAM-INF:BANDWIDTH=4000000
  → /stream/segment/QmHigh.m3u8
```

### Безопасность: верификация размера файла

Сервис не доверяет `Content-Length` из HTTP-заголовка — реальный размер подсчитывается сервером через `countingReader` при чтении потока. После загрузки — дополнительная проверка через `ClusterStat()`, который запрашивает реальный размер из IPFS DAG. Если файл превышает лимит — он анпиннится и возвращается 413.

### Аутентификация

Сервис поддерживает двухуровневую проверку подлинности:

```
Запрос → [1] Статические API-ключи (API_KEYS из env, если заданы)
           ├→ совпадение → 200
           └→ нет
         [2] Lua authorize(req) если AUTH_LUA_SCRIPT задан
           ├→ true → 200
           └→ false / ошибка / таймаут → 401
```

**Статические ключи** — опциональны, не требуют внешних сервисов. Указываются в `API_KEYS` через запятую. Если `API_KEYS` пустой, статическая проверка отключена.

**Lua-скрипт** — опциональный fallback для интеграции с любым auth-провайдером (JWT-сервис, OAuth, Keycloak и т.д.). Скрипт получает весь HTTP-реквест и возвращает `true` (доступ разрешён) или `false` (отказ).

Пример — интеграция с auth-service:

```lua
local request = require("request")
local json    = require("json")
local env     = require("env")

function authorize(req)
  local token = req.headers["Authorization"]
  if not token or token == "" then
    token = req.headers["X-API-Key"]
  end
  if not token or token == "" then
    token = req.query["token"] or req.query["access_token"]
    if token and token ~= "" then
      token = "Bearer " .. token
    end
  end
  if not token or token == "" then return false end

  local url = env.get("AUTH_SERVICE_URL")
  if not url then return false end

  local resp = request.get(url .. "/auth/me", {
    headers = { Authorization = token }
  })

  if not resp or resp.status ~= 200 then return false end
  local data = json.decode(resp.body)
  return data.id ~= nil
end
```

#### Lua sandbox

Скрипт выполняется в изолированной среде:

• Доступны только безопасные стандартные библиотеки: `base`, `math`, `string`, `table`
• Заблокированы: `io`, `os`, `file`, `debug`, `coroutine`
• Таймаут выполнения — `AUTH_LUA_TIMEOUT_MS` (default 3000), при превышении VM прерывается

#### Доступные Lua-библиотеки

| Библиотека | Методы | Описание |
|---|---|---|
| `request` | `get(url, opts)`, `post(url, opts)`, `put(url, opts)`, `del(url, opts)` | HTTP-клиент с таймаутом. opts = {headers={}, body=""} |
| `json` | `encode(table)`, `decode(string)` | JSON кодирование/декодирование |
| `env` | `get("KEY")` | Только из белого списка (`AUTH_LUA_ENV_WHITELIST`) |

#### Структура req в Lua

| Поле | Тип | Описание |
|---|---|---|
| `method` | string | HTTP метод |
| `path` | string | Путь URL |
| `headers` | table | Заголовки запроса (ключ → последнее значение) |
| `query` | table | Query-параметры (ключ → последнее значение) |
| `remote_addr` | string | IP:port клиента |

## Возможности

- 📤 Загрузка одного файла — `POST /upload`
- 📦 Массовая загрузка — `POST /upload-multiple`
- 🖼 Image bundles — оригинал, privacy-копии и настроенные размеры (`/file/{cid}/{size}`) с metadata manifest
- 🎬 Видео-стриминг — `POST /upload-video` → адаптивный HLS (3 качества)
- 📺 Воспроизведение — `GET /stream/{cid}/master.m3u8`
- 📥 Скачивание по CID — `GET /file/{cid}`
- ⚙️ Публичная конфигурация возможностей — `GET /config`
- 🗑 Мягкое удаление — `DELETE /file/{cid}` (unpin + TTL)
- 🔄 Кластерная репликация — автоматический Fetch+Pin на все ноды
- 🔐 Аутентификация — статические API-ключи + опциональный Lua-скрипт
- 🛡 Валидация файлов — расширение, MIME-тип, размер (серверная верификация)
- 🎥 Валидация видео — длительность, пропорции 9:16, размер
- 🌍 CORS — настраиваемые origin и заголовки
- ♻️ Фоновый GC — удаление истёкших файлов по TTL

## Быстрый старт

```bash
git clone https://github.com/borg001/ipfs-filestorage.git
cd ipfs-filestorage

# Конфигурация
cp .env.example .env
# nano .env  — поменяйте API_KEYS и параметры при необходимости

# Запуск
docker compose up --build -d
```

API будет доступен на `http://localhost:${NGINX_PORT:-8081}` (nginx).

Порты публикации контейнеров на хост настраиваются через `.env`:

```env
STORAGE1_PORT=3001
STORAGE2_PORT=3002
NGINX_PORT=8081
```

Подождите ~60 секунд — IPFS-нодам нужно время на инициализацию, healthcheck и подключение к bootstrap.

### Проверка работоспособности

```bash
# Проверить, что все контейнеры здоровы
docker ps --format "table {{.Names}}\t{{.Status}}"

# Проверить репликацию — загрузить файл на storage1, скачать с storage2
curl -s -X POST http://localhost:3001/upload \
  -H "X-API-Key: SECRET_KEY_1" \
  -F "file=@test.json;type=application/json" | jq .

# Ответ: {"cid":"QmXyZ...","name":"test.json","size":42,"pinned":true,"type":"file",...}

# Прочитать тот же CID через storage2
curl -s http://localhost:3002/file/QmXyZ... \
  -H "X-API-Key: SECRET_KEY_1" -o /dev/null -w "%{http_code}"
# 200 — репликация работает
```

Для браузерных media URL, где нельзя добавить `Authorization` header, можно передать токен в query string:

```bash
curl -s "http://localhost:8081/file/QmXyZ...?token=SECRET_KEY_1" -o file.bin
```

### Загрузка видео

```bash
# Загрузить вертикальное видео (9:16, до 30 МБ, до 60 сек)
curl -s -X POST http://localhost:8081/upload-video \
  -H "X-API-Key: SECRET_KEY_1" \
  -F "file=@clip.mp4" | jq .

# Ответ:
# {
#   "master_cid": "QmAbCd...",
#   "variant_cids": {
#     "low": "QmLo1...",
#     "medium": "QmLo2...",
#     "high": "QmLo3..."
#   },
#   "poster_cids": {"180x320": "QmPoster..."},
#   "privacy_poster_cids": {
#     "blur": {"180x320": "QmBlurPoster..."},
#     "blur_faces": {"180x320": "QmFaceBlurPoster..."}
#   },
#   "status": "processing_done"
# }

# Воспроизвести в HLS-плеере (hls.js, VLC, ffplay)
ffplay http://localhost:8081/stream/QmAbCd.../master.m3u8
```

### Подключение Lua-авторизации

```bash
# 1. Напишите скрипт (или используйте scripts/auth_example.lua)
# 2. Укажите путь в .env:
AUTH_LUA_SCRIPT=/app/scripts/auth_example.lua
AUTH_LUA_ENV_WHITELIST=AUTH_SERVICE_URL
# 3. Перезапустите сервис:
docker compose up --build -d
```

## Конфигурация

Задаётся через `.env` файл (шаблон: `.env.example`).

### Основные параметры

| Переменная | По умолчанию | Описание |
|---|---|---|
| `SERVER_PORT` | `3000` | Порт HTTP-сервера внутри контейнера |
| `STORAGE1_PORT` | `3001` | Порт `storage1` на хосте для прямого доступа |
| `STORAGE2_PORT` | `3002` | Порт `storage2` на хосте для прямого доступа |
| `NGINX_PORT` | `8081` | Порт nginx-балансировщика на хосте |
| `IPFS_URL` | `http://localhost:5001` | URL локальной IPFS-ноды (переопределяется в docker-compose) |
| `CLUSTER_NODES` | `http://ipfs1:5001,http://ipfs2:5001` | Адреса всех нод кластера через запятую |
| `API_KEYS` | empty | Опциональные статические API-ключи через запятую |
| `UPLOAD_MAX_FILE_SIZE` | `10485760` (10 МБ) | Максимальный размер файла |
| `UPLOAD_ALLOWED_EXTENSIONS` | `png,svg,jpg,pdf,doc,docx,zip,json,html,txt,mp4,mov,webm,avi` | Разрешённые расширения |
| `PINNING_RETRIES` | `3` | Попыток пиннинга при репликации |
| `PINNING_RETRY_DELAY_MS` | `1000` | Задержка между попытками (мс) |
| `UNPIN_TTL` | `24h` | Время до физического удаления после soft-delete |
| `UNPIN_GC_INTERVAL` | `1h` | Интервал проверки GC-воркера |
| `UNPIN_STORE_PATH` | `/data/unpin-store.json` | Путь к файлу unpin-списка |
| `CORS_ALLOWED_ORIGINS` | `*` | Разрешённые origins |
| `CORS_ALLOWED_HEADERS` | `Origin,X-Requested-With,Content-Type,Accept,X-API-Key,Authorization` | Разрешённые заголовки |

### Параметры image bundle

| Переменная | По умолчанию | Описание |
|---|---|---|
| `IMAGE_PROCESSING_ENABLED` | `true` | Генерировать image variants при загрузке raster image |
| `IMAGE_VARIANTS` | `100x100,320x320,480x640,640x640,768x1024,1024x1024` | Размеры вариантов через запятую |
| `IMAGE_OUTPUT_FORMAT` | `auto` | `auto`, `jpeg` или `webp`. В `auto` PNG/WebP variants пишутся в WebP, остальные в JPEG |
| `IMAGE_JPEG_PROGRESSIVE` | `true` | Писать JPEG variants как progressive JPEG через ffmpeg |
| `IMAGE_JPEG_QUALITY` | `82` | Качество JPEG variants, 1-100 |
| `IMAGE_WEBP_QUALITY` | `82` | Качество WebP variants, 1-100 |
| `IMAGE_RESIZE_POLICY` | `smart-cover` | `fit`, `cover-center`, `smart-cover`. Сейчас `smart-cover` использует center cover и оставлен как точка расширения под face/saliency crop |
| `IMAGE_BLUR_RADIUS` | `24` | Радиус полного blur-варианта для закрытого изображения |
| `IMAGE_FACE_BLUR_RADIUS` | `16` | Минимальный радиус blur по каждой найденной области лица |
| `IMAGE_FACE_DETECTION_MAX_DIMENSION` | `1280` | Максимальная сторона копии, на которой работает детектор; большие изображения пропорционально уменьшаются только для поиска лиц |
| `IMAGE_FACE_DETECTION_SCORE_THRESHOLD` | `0.8` | Минимальная confidence YuNet. Более высокий порог уменьшает ложные blur-области, но может пропустить мелкие или повёрнутые лица |
| `IMAGE_FACE_DETECTION_NMS_THRESHOLD` | `0.3` | Порог NMS YuNet для объединения пересекающихся рамок лиц |

Каждая raster-фотография получает два обязательных privacy-варианта, независимо от `IMAGE_VARIANTS`:

| Ключ | URL | Назначение |
|---|---|---|
| `blur` | `/file/{cid}/blur` | Полностью размытая копия. Её нужно использовать при отсутствии права на просмотр оригинала. |
| `blur_faces` | `/file/{cid}/blur_faces` | Копия с размытыми областями лиц, найденных локальным детектором. Если лицо не найдено, вариант безопасно становится полным `blur`. |

`blur_faces` — средство визуальной приватности, а не механизм авторизации: распознавание не может гарантировать обнаружение любого лица. Доступ к оригиналу и видео всегда должен решаться вызывающим сервисом; для отказа в доступе он возвращает только `blur` и не раскрывает URL оригинала.

Детектор — встроенная ONNX-модель [YuNet](https://github.com/opencv/opencv_zoo/tree/main/models/face_detection_yunet), запущенная локально через OpenCV `FaceDetectorYN`. Модель и OpenCV runtime входят в Docker-образ, поэтому во время upload не выполняются запросы к внешним сервисам. Для стабильной работы требуется OpenCV 4.10, уже зафиксированный в `Dockerfile`. Лицензионное уведомление находится в [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

### Параметры видео

| Переменная | По умолчанию | Описание |
|---|---|---|
| `VIDEO_MAX_DURATION_SEC` | `60` | Максимальная длительность видео (секунд) |
| `VIDEO_MAX_SIZE_MB` | `30` | Максимальный размер видеофайла (МБ) |
| `VIDEO_ASPECT_RATIO_TOLERANCE` | `0.1` | Допуск пропорций от 9:16 (0.1 = ±10%) |
| `VIDEO_SEGMENT_DURATION_SEC` | `4` | Длительность HLS-сегмента (секунд) |
| `VIDEO_BITRATES` | `500k,1500k,4000k` | Битрейты для low/medium/high вариантов |
| `FFMPEG_PATH` | `ffmpeg` | Путь к бинарнику ffmpeg |
| `FFPROBE_PATH` | `ffprobe` | Путь к бинарнику ffprobe |
| `VIDEO_TEMP_DIR` | `/tmp/video_processing` | Директория для временных файлов транскодирования |
| `VIDEO_THUMBNAIL_VARIANTS` | `180x320,360x640,720x1280` | Размеры poster JPEG для видео |
| `VIDEO_THUMBNAIL_TIME_SEC` | `1.0` | Секунда видео для генерации poster |
| `VIDEO_THUMBNAIL_QSCALE` | `3` | Качество JPEG poster для ffmpeg |

### Параметры Lua-авторизации

| Переменная | По умолчанию | Описание |
|---|---|---|
| `AUTH_LUA_SCRIPT` | _(пусто)_ | Путь к .lua файлу. Пусто = Lua отключён, только статические ключи |
| `AUTH_LUA_TIMEOUT_MS` | `3000` | Таймаут выполнения скрипта (мс) |
| `AUTH_LUA_ENV_WHITELIST` | `AUTH_SERVICE_URL` | Разрешённые env-переменные для `env.get()` через запятую |

## API

Все эндпоинты, кроме публичного `GET /config`, требуют аутентификацию: заголовок `Authorization: Bearer <token>`, `X-API-Key: <key>` или query-параметр `?token=<token>` / `?access_token=<token>` для браузерных media-запросов.

### Публичная конфигурация

```http
GET /config
```

Возвращает публичные возможности сервиса. Секция `image` описывает доступные размеры, privacy-варианты и шаблоны URL.

```json
{
  "image": {
    "enabled": true,
    "variants": [{"key": "100x100", "width": 100, "height": 100}],
    "privacy_variants": [
      {"key": "blur", "purpose": "full_image_blur"},
      {"key": "blur_faces", "purpose": "detected_faces_blur", "fallback": "blur"}
    ],
    "output_format": "auto",
    "jpeg_progressive": true,
    "resize_policy": "smart-cover",
    "url_template": "/file/{cid}/{size}",
    "bundle_template": "/file/{cid}/bundle"
  }
}
```

### Загрузка файла

```http
POST /upload
Content-Type: multipart/form-data
Authorization: Bearer SECRET_KEY_1
```

Поле формы: `file`

Ответ:
```json
{
  "cid": "QmXyZ...",
  "name": "avatar.png",
  "size": 245760,
  "pinned": true,
  "type": "image",
  "original": {
    "path": "/file/QmXyZ...",
    "bundle_path": "original",
    "format": "png",
    "content_type": "image/png",
    "width": 1200,
    "height": 900,
    "size": 245760
  },
  "variants": {
    "100x100": {
      "path": "/file/QmXyZ.../100x100",
      "bundle_path": "100x100.webp",
      "format": "webp",
      "content_type": "image/webp",
      "width": 100,
      "height": 100,
      "size": 8920
    },
    "blur": {
      "path": "/file/QmXyZ.../blur",
      "bundle_path": "blur.webp",
      "format": "webp",
      "content_type": "image/webp",
      "width": 1200,
      "height": 900,
      "size": 34120
    },
    "blur_faces": {
      "path": "/file/QmXyZ.../blur_faces",
      "bundle_path": "blur_faces.webp",
      "format": "webp",
      "content_type": "image/webp",
      "width": 1200,
      "height": 900,
      "size": 49510
    }
  }
}
```

Размер в ответе — реальный размер прочитанных сервером байтов, а не заголовок от клиента.

`cid` в ответе — это root CID bundle-директории. Для любых типов файлов внутри bundle хранится `original` и `manifest.json`. Для изображений дополнительно сохраняются privacy-варианты и настроенные размеры.

### Массовая загрузка

```http
POST /upload-multiple
Content-Type: multipart/form-data
Authorization: Bearer SECRET_KEY_1
```

Поле формы: `file` (несколько файлов с одним именем поля)

Ответ: массив объектов как при одиночной загрузке.

### Загрузка видео

```http
POST /upload-video
Content-Type: multipart/form-data
Authorization: Bearer SECRET_KEY_1
```

Поле формы: `file` (mp4, webm, mov, avi, mkv, m4v)

Ограничения: вертикальное видео (9:16 ±10%), до 30 МБ, до 60 сек.

Ответ:
```json
{
  "master_cid": "QmAbCd...",
  "variant_cids": {
    "low": "QmLo1...",
    "medium": "QmLo2...",
    "high": "QmLo3..."
  },
  "poster_cids": {
    "180x320": "QmPoster..."
  },
  "privacy_poster_cids": {
    "blur": {"180x320": "QmBlurPoster..."},
    "blur_faces": {"180x320": "QmFaceBlurPoster..."}
  },
  "duration_sec": 11.4,
  "status": "processing_done"
}
```

Каждый poster получает те же `blur` и `blur_faces` копии, что и фотография. В master playlist оригинальные poster-записи сохраняют прежний формат, а privacy-варианты добавляются отдельными строками:

```text
#EXT-X-IAMFREE-POSTER:SIZE=180x320,URI="../segment/QmPoster.jpg"
#EXT-X-IAMFREE-POSTER:VARIANT=blur,SIZE=180x320,URI="../segment/QmBlurPoster.jpg"
#EXT-X-IAMFREE-POSTER:VARIANT=blur_faces,SIZE=180x320,URI="../segment/QmFaceBlurPoster.jpg"
```

### Стриминг видео

```http
GET /stream/{masterCID}/master.m3u8
Authorization: Bearer SECRET_KEY_1
```

Возвращает мастер-плейлист HLS с тремя уровнями качества.

```http
GET /stream/segment/{cid}.m3u8
GET /stream/segment/{cid}.m4s
```

Возвращает вариантный плейлист или чанк сегмента. Content-Type определяется по расширению.

### Скачивание файла

```http
GET /file/{cid}
Authorization: Bearer SECRET_KEY_1
```

Возвращает оригинальный файл из bundle с корректным `Content-Type`.

```http
GET /file/{cid}/bundle
Authorization: Bearer SECRET_KEY_1
```

Возвращает manifest bundle. Его можно получить повторно по root CID, поэтому клиентам не обязательно хранить весь JSON ответа загрузки.

```http
GET /file/{cid}/{size}
Authorization: Bearer SECRET_KEY_1
```

Возвращает image variant, например `/file/{cid}/100x100`. Если variant не был сгенерирован, вернётся `404`.

### Удаление файла (мягкое)

```http
DELETE /file/{cid}
Authorization: Bearer SECRET_KEY_1
```

Файл немедленно помечается как удалённый. `GET /file/{cid}` возвращает `404`. Физическое удаление — после истечения `UNPIN_TTL`.

Ответ:
```json
{
  "status": "deleted",
  "cid": "QmXyZ..."
}
```

Для видео: удаление по `masterCID` удаляет все связанные чанки и плейлисты (групповое удаление через AddGroup/RemoveGroup).

## Тесты

### Юнит-тесты

```bash
# Рекомендуемый способ: фиксированные Go 1.23 и OpenCV 4.10 из Dockerfile.
docker build --target test .

# Локальный запуск возможен при установленном OpenCV 4.10 и Go 1.23.
go test ./internal/... -count=1 -v
```

YuNet-покрытие включает портрет с одним лицом, изображение без лица, генерацию
`blur`/`blur_faces` и video poster flow. Фото без лица защищает от возврата
ложного распознавания объекта как лица.

Покрытие:

| Пакет | Что тестируется |
|---|---|
| `internal/auth/lua` | Disabled, authorize true/false, no function, request fields, timeout, env whitelist/blocked, HTTP request, JSON, syntax error |
| `internal/auth/static` | Valid key, bearer, no token, invalid key, empty keys |
| `internal/config` | Дефолты, env-override, невалидные значения, float-парсинг |
| `internal/imageproc` | YuNet face detection, privacy-варианты, fallback на полный blur, ellipse blur |
| `internal/handler` | Upload (лимит, oversized, подделка размера), Video upload (no file, wrong ext, too large), Stream (master, segment, 404, deleted) |
| `internal/ipfs` | Cluster Add/Replicate/Cat/Unpin, Stat (DAG size), countingReader |
| `internal/middleware` | PanicRecovery, CORS, Chain, Lua fallback, context |
| `internal/store` | AddGroup/GetGroup/RemoveGroup, concurrent access (50 горутин), persistence, legacy format |
| `internal/video` | Validator (size, duration, aspect ratio, probe error), Uploader (rewrite playlists, CID substitution, master playlist), Transcoder (HLS args, bitrate params, segment naming) |

### Интеграционные тесты

Тесты запускаются на хосте (нужен Go 1.22+) и работают с запущенным кластером через проброшенные порты.

```bash
# Убедитесь, что кластер запущен
docker compose up --build -d

# Подождите ~90 секунд для прогрева IPFS

# Запуск тестов
INTEGRATION=1 go test ./tests/integration/... -tags=integration -v -timeout 180s
```

| Тест | Что проверяет |
|------|---------------|
| `TestReplicationByteForByte` | Upload → данные byte-for-byte идентичны на ipfs2 через 5с |
| `TestReplicationMultipleSizes` | 1KB, 10KB, 100KB, 1MB — все реплицируются |
| `TestReplicationNodeBRestart` | После рестарта ipfs2 данные доступны и запиннены |
| `TestReplicationBothNodesHaveData` | Обе ноды содержат идентичные данные, оба pin активны |
| `TestReplicationAfterDelete` | Soft-delete → GET возвращает 404, данные сохранены на ipfs2 |
| `TestUploadAndDownload` | Полный цикл: upload → download → delete → 404 |
| `TestUploadMultiple` | Массовая загрузка нескольких файлов |
| `TestAuth` | Запрос без ключа → 401, с неверным ключом → 401, с верным → 200 |

## Внутреннее устройство

### Пакеты Go

```
cmd/server/          — точка входа, инициализация зависимостей
internal/
  auth/
    provider.go      — интерфейс Provider + Result
    static/          — статические API-ключи из env
    lua/             — sandboxed Lua VM: request, json, env
  config/            — парсинг .env, дефолты
  handler/
    handler.go       — HTTP-обработчики (upload, download, delete)
    video_handler.go — Видео: upload-video, stream, segment
  ipfs/
    client.go        — обёртка над Kubo HTTP API (Add, Cat, Pin, Unpin, Stat, Fetch)
    cluster.go       — ClusterManager: репликация, unpin, проверки
    clusterer.go     — интерфейс Clusterer (для mock в тестах)
    helpers.go       — утилиты (dns4-префикс для multiaddr и пр.)
    named_reader.go  — io.Reader с именем файла
  middleware/         — Auth (static + Lua fallback), CORS, логирование
  store/              — UnpinStore: файловый store + GC-воркер + группы
  video/
    transcoder.go    — ffmpeg: транскодирование → HLS/CMAF (3 качества)
    validator.go     — ffprobe: валидация (размер, длительность, 9:16)
    uploader.go      — загрузка чанков в IPFS, переписывание плейлистов
tests/
  integration/       — E2E тесты (требуют запущенный кластер)
scripts/
  auth_example.lua   — пример Lua-скрипта для интеграции с auth-service
```

### Clusterer interface

Интерфейс `Clusterer` позволяет подменять реализацию кластера в юнит-тестах через mock, не поднимая реальный IPFS.

### DNS в multiaddr

Docker-сети используют DNS-имена контейнеров (например, `ipfs1`). Стандартная функция Kubo `httpURLToMultiaddr` не поддерживает DNS — она ожидает IP-адрес. Поэтому для Docker-хостов используется префикс `/dns4/`:

`/dns4/ipfs1/tcp/5001/p2p/12D3KooW...` вместо `/ip4/172.20.0.2/tcp/5001/p2p/...`

### Групповое удаление видео

При загрузке видео вызывается `AddGroup(masterCID, allCIDs)`. Это связывает мастер-плейлист со всеми чанками и вариантными плейлистами. При `DELETE /file/{masterCID}` вызывается `RemoveGroup()`, который анпиннит все связанные CID на всех нодах.

## Масштабирование

Для добавления новой ноды:

1. Добавить сервисы `ipfs3` и `storage3` в `docker-compose.yml`
2. Добавить URL `http://ipfs3:5001` в `CLUSTER_NODES` в `.env`
3. Добавить отдельный volume для unpin-store (`unpin-data-3`)
4. Пересобрать: `docker compose up --build -d`

## Лицензия

MIT
