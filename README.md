# IPFS File Storage

HTTP-сервис для децентрализованного хранения файлов поверх [IPFS](https://ipfs.tech/) (InterPlanetary File System) с поддержкой кластерной репликации и мягким удалением через механизм unpin.

## Архитектура

```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │ HTTP
       ▼
┌─────────────┐     ┌─────────────┐
│    nginx    │────▶│  storage1   │
│  :8080      │     │  :3000      │
│  round-robin│────▶│  → ipfs1    │
└─────────────┘     └─────────────┘
       │
       └────────────▶┌─────────────┐
                     │  storage2   │
                     │  :3000      │
                     │  → ipfs2    │
                     └─────────────┘
```

- **nginx** — балансировщик нагрузки (round-robin)
- **storage1/storage2** — Go-сервисы, каждый со своей IPFS-нодой
- **ipfs1/ipfs2** — Kubo-ноды в приватном swarm, реплицируют файлы друг на друга

## Возможности

- 📤 **Загрузка одного файла** — `POST /upload`
- 📦 **Массовая загрузка** — `POST /upload-multiple`
- 📥 **Скачивание по CID** — `GET /file/{cid}`
- 🗑 **Мягкое удаление** — `DELETE /file/{cid}` (unpin + TTL)
- 🔄 **Кластерная репликация** — автоматический pin на все ноды
- 🔐 **API-Key аутентификация**
- 🛡 **Валидация файлов** — расширение, MIME-тип, размер
- 🌍 **CORS** — настраиваемые origin и заголовки
- ♻️ **Фоновый GC** — удаление истёкших файлов по TTL

## Быстрый старт (Docker Compose)

```bash
git clone https://github.com/borg001/ipfs-filestorage.git
cd ipfs-filestorage
docker-compose up --build
```

API будет доступен на `http://localhost:8080`.

## Конфигурация

| Переменная | По умолчанию | Описание |
|---|---|---|
| `SERVER_PORT` | `3000` | Порт HTTP-сервера |
| `IPFS_URL` | `http://localhost:5001` | URL локальной IPFS-ноды |
| `CLUSTER_NODES` | `http://localhost:5001` | Все ноды кластера через запятую |
| `API_KEYS` | `SECRET_KEY_1,SECRET_KEY_2` | API-ключи через запятую |
| `UPLOAD_MAX_FILE_SIZE` | `10485760` (10 МБ) | Максимальный размер файла |
| `UPLOAD_ALLOWED_EXTENSIONS` | `png,svg,jpg,pdf,doc,docx,zip,json,html` | Разрешённые расширения |
| `PINNING_RETRIES` | `3` | Попыток пиннинга |
| `PINNING_RETRY_DELAY_MS` | `1000` | Задержка между попытками (мс) |
| `UNPIN_TTL` | `24h` | TTL после удаления до физического GC |
| `UNPIN_GC_INTERVAL` | `1h` | Интервал запуска GC-воркера |
| `UNPIN_STORE_PATH` | `/data/unpin-store.json` | Путь к хранилищу unpin-списка |
| `CORS_ALLOWED_ORIGINS` | `*` | Разрешённые origins |
| `CORS_ALLOWED_HEADERS` | `Origin,...,X-API-Key` | Разрешённые заголовки |

## API

Все эндпоинты требуют заголовок `X-API-Key`.

### Загрузка файла

```http
POST /upload
Content-Type: multipart/form-data
X-API-Key: SECRET_KEY_1
```

**Ответ:**
```json
{
  "cid": "QmXyZ...",
  "name": "document.pdf",
  "size": 245760,
  "pinned": true
}
```

### Скачивание файла

```http
GET /file/{cid}
X-API-Key: SECRET_KEY_1
```

Возвращает бинарное содержимое с `Content-Type`.

### Удаление файла (мягкое)

```http
DELETE /file/{cid}
X-API-Key: SECRET_KEY_1
```

Файл немедленно помечается как удалённый (unpin-список). `GET /file/{cid}` начинает возвращать `404`. Физическое удаление происходит после истечения `UNPIN_TTL` фоновым GC-воркером.

## Механизм удаления

1. **DELETE /file/{cid}** — файл добавляется в `unpin-список` с timestamp
2. **GET /file/{cid}** — проверяет unpin-список, возвращает `404` если файл удалён
3. **Фоновый GC** — каждые `UNPIN_GC_INTERVAL` проверяет истёкшие записи
4. **Физический unpin** — после `UNPIN_TTL` файл анпиннится на всех нодах кластера

## Репликация

При загрузке файл добавляется на одну IPFS-ноду, затем автоматически реплицируется (pin) на **все ноды кластера** параллельно. Это обеспечивает отказоустойчивость — файл доступен даже при падении одной ноды.

## Лицензия

MIT
