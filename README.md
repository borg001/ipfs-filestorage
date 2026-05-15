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
  nginx     → :8081   (балансировщик)
  storage1  → :3001   (API напрямую)
  storage2  → :3002   (API напрямую)
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
POST /upload → storage1 → ipfs1.Add() → CID
                                       ↓
                         ClusterReplicate(CID):
                           ├→ ipfs1: Fetch(Cat+drain) + Pin  (локально — быстро)
                           └→ ipfs2: Fetch(Cat+drain) + Pin  (bitswap через DHT)
```

Ключевое: `Fetch()` вызывает `Cat()` и вычитывает весь поток. Это заставляет bitswap подтянуть ВСЕ блоки DAG с ноды-источника. После этого `Pin()` гарантирует, что данные физически на ноде. Без Fetch — Pin создаст маркер без реальных данных.

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
  ├→ transcode: ffmpeg — 3 варианта (low/medium/high), fMP4 CMAF
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

## Возможности

- 📤 Загрузка одного файла — `POST /upload`
- 📦 Массовая загрузка — `POST /upload-multiple`
- 🎬 Видео-стриминг — `POST /upload-video` → адаптивный HLS (3 качества)
- 📺 Воспроизведение — `GET /stream/{cid}/master.m3u8`
- 📥 Скачивание по CID — `GET /file/{cid}`
- 🗑 Мягкое удаление — `DELETE /file/{cid}` (unpin + TTL)
- 🔄 Кластерная репликация — автоматический Fetch+Pin на все ноды
- 🔐 API-Key аутентификация — заголовок `X-API-Key`
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

API будет доступен на `http://localhost:8081` (nginx).

Подождите ~60 секунд — IPFS-нодам нужно время на инициализацию, healthcheck и подключение к bootstrap.

### Проверка работоспособности

```bash
# Проверить, что все контейнеры здоровы
docker ps --format "table {{.Names}}\t{{.Status}}"

# Проверить репликацию — загрузить файл на storage1, скачать с storage2
curl -s -X POST http://localhost:3001/upload \
  -H "X-API-Key: SECRET_KEY_1" \
  -F "file=@test.json;type=application/json" | jq .

# Ответ: {"cid":"QmXyZ...","name":"test.json","size":42,"pinned":true}

# Прочитать тот же CID через storage2
curl -s http://localhost:3002/file/QmXyZ... \
  -H "X-API-Key: SECRET_KEY_1" -o /dev/null -w "%{http_code}"
# 200 — репликация работает
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
#   "variants": {
#     "low": "QmLo1...",
#     "medium": "QmLo2...",
#     "high": "QmLo3..."
#   },
#   "all_cids": ["QmAbCd...", "QmLo1...", ...],
#   "pinned": true
# }

# Воспроизвести в HLS-плеере (hls.js, VLC, ffplay)
ffplay http://localhost:8081/stream/QmAbCd.../master.m3u8
```

## Конфигурация

Задаётся через `.env` файл (шаблон: `.env.example`).

### Основные параметры

| Переменная | По умолчанию | Описание |
|---|---|---|
| `SERVER_PORT` | `3000` | Порт HTTP-сервера внутри контейнера |
| `IPFS_URL` | `http://localhost:5001` | URL локальной IPFS-ноды (переопределяется в docker-compose) |
| `CLUSTER_NODES` | `http://ipfs1:5001,http://ipfs2:5001` | Адреса всех нод кластера через запятую |
| `API_KEYS` | `SECRET_KEY_1,SECRET_KEY_2` | API-ключи через запятую |
| `UPLOAD_MAX_FILE_SIZE` | `10485760` (10 МБ) | Максимальный размер файла |
| `UPLOAD_ALLOWED_EXTENSIONS` | `png,svg,jpg,pdf,doc,docx,zip,json,html` | Разрешённые расширения |
| `PINNING_RETRIES` | `3` | Попыток пиннинга при репликации |
| `PINNING_RETRY_DELAY_MS` | `1000` | Задержка между попытками (мс) |
| `UNPIN_TTL` | `24h` | Время до физического удаления после soft-delete |
| `UNPIN_GC_INTERVAL` | `1h` | Интервал проверки GC-воркера |
| `UNPIN_STORE_PATH` | `/data/unpin-store.json` | Путь к файлу unpin-списка |
| `CORS_ALLOWED_ORIGINS` | `*` | Разрешённые origins |
| `CORS_ALLOWED_HEADERS` | `Origin,X-Requested-With,Content-Type,Accept,X-API-Key` | Разрешённые заголовки |

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
| `VIDEO_TEMP_DIR` | `/tmp/ipfs-video` | Директория для временных файлов транскодирования |

## API

Все эндпоинты требуют заголовок `X-API-Key`.

### Загрузка файла

```http
POST /upload
Content-Type: multipart/form-data
X-API-Key: SECRET_KEY_1
```

Поле формы: `file`

Ответ:
```json
{
  "cid": "QmXyZ...",
  "name": "document.pdf",
  "size": 245760,
  "pinned": true
}
```

Размер в ответе — реальный размер прочитанных сервером байтов, а не заголовок от клиента.

### Массовая загрузка

```http
POST /upload-multiple
Content-Type: multipart/form-data
X-API-Key: SECRET_KEY_1
```

Поле формы: `file` (несколько файлов с одним именем поля)

Ответ: массив объектов как при одиночной загрузке.

### Загрузка видео

```http
POST /upload-video
Content-Type: multipart/form-data
X-API-Key: SECRET_KEY_1
```

Поле формы: `file` (mp4, webm, mov, avi, mkv, m4v)

Ограничения: вертикальное видео (9:16 ±10%), до 30 МБ, до 60 сек.

Ответ:
```json
{
  "master_cid": "QmAbCd...",
  "variants": {
    "low": "QmLo1...",
    "medium": "QmLo2...",
    "high": "QmLo3..."
  },
  "all_cids": ["QmAbCd...", "QmLo1...", "QmLo2...", "QmLo3...", "..."],
  "pinned": true
}
```

### Стриминг видео

```http
GET /stream/{masterCID}/master.m3u8
X-API-Key: SECRET_KEY_1
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
X-API-Key: SECRET_KEY_1
```

Возвращает бинарное содержимое с корректным `Content-Type`.

### Удаление файла (мягкое)

```http
DELETE /file/{cid}
X-API-Key: SECRET_KEY_1
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
go test ./internal/... -count=1 -v
```

Покрытие:

| Пакет | Что тестируется |
|---|---|
| `internal/config` | Дефолты, env-override, невалидные значения, float-парсинг |
| `internal/handler` | Upload (лимит, oversized, подделка размера), Video upload (no file, wrong ext, too large), Stream (master, segment, 404, deleted) |
| `internal/ipfs` | Cluster Add/Replicate/Cat/Unpin, Stat (DAG size), countingReader |
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
  middleware/         — APIKey auth, CORS, логирование
  store/              — UnpinStore: файловый store + GC-воркер + группы
  video/
    transcoder.go    — ffmpeg: транскодирование → HLS/CMAF (3 качества)
    validator.go     — ffprobe: валидация (размер, длительность, 9:16)
    uploader.go      — загрузка чанков в IPFS, переписывание плейлистов
tests/
  integration/       — E2E тесты (требуют запущенный кластер)
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
