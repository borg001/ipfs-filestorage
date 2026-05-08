# Storage v1

HTTP-сервис для децентрализованного хранения файлов поверх [IPFS](https://ipfs.tech/) (InterPlanetary File System). Загружайте, скачивайте и управляйте файлами через REST API с аутентификацией по API-ключам.

## Возможности

- 📤 **Загрузка одного файла** — `POST /upload`
- 📦 **Массовая загрузка** — `POST /upload-multiple`
- 📥 **Скачивание по CID** — `GET /file/{cid}`
- 🔐 **API-Key аутентификация** — защита всех эндпоинтов
- 🛡 **Валидация файлов** — фильтрация по расширению и MIME-типу
- 🌍 **CORS** — настраиваемые origin и заголовки
- 🔁 **Retry-логика пиннинга** — экспоненциальная задержка с настраиваемым количеством попыток
- ⚙️ **Конфигурация через переменные окружения** — 12-Factor App

## Структура проекта

```
storage-v1/
├── cmd/
│   └── server/
│       └── main.go              # Точка входа, сборка зависимостей
├── internal/
│   ├── config/
│   │   └── config.go            # Загрузка конфигурации из env
│   ├── handler/
│   │   └── handler.go           # HTTP-обработчики (upload/download)
│   ├── ipfs/
│   │   └── client.go            # Обёртка над IPFS RPC-клиентом
│   └── middleware/
│       └── middleware.go        # CORS, API-Key Auth, Panic Recovery
├── go.mod
└── README.md
```

## Требования

- **Go** 1.22+
- **IPFS-нода** (Kubo) с включённым RPC API (порт `5001` по умолчанию)

## Быстрый старт

### 1. Клонирование

```bash
git clone https://github.com/borg001/ipfs-filestorage.git
cd ipfs-filestorage
```

### 2. Установка зависимостей

```bash
go mod download
```

### 3. Запуск IPFS-ноды

```bash
ipfs daemon
```

### 4. Запуск сервиса

```bash
# С настройками по умолчанию
go run ./cmd/server

# Кастомный порт и IPFS-нода
SERVER_PORT=8080 IPFS_URL=http://127.0.0.1:5001 go run ./cmd/server
```

Сервис запустится на `http://localhost:3000`.

## Конфигурация

Все параметры задаются через переменные окружения.

| Переменная | По умолчанию | Описание |
|---|---|---|
| `SERVER_PORT` | `3000` | Порт HTTP-сервера |
| `IPFS_URL` | `http://localhost:5001` | URL IPFS RPC API |
| `API_KEYS` | `SECRET_KEY_1,SECRET_KEY_2` | Список API-ключей (через запятую) |
| `UPLOAD_MAX_FILE_SIZE` | `10485760` (10 МБ) | Максимальный размер файла в байтах |
| `UPLOAD_ALLOWED_EXTENSIONS` | `png,svg,jpg,pdf,doc,docx,zip,json,html` | Разрешённые расширения |
| `CORS_ALLOWED_ORIGINS` | `*` | Разрешённые origin (через запятую) |
| `CORS_ALLOWED_HEADERS` | `Origin, X-Requested-With, Content-Type, Accept, X-API-Key` | Разрешённые заголовки |
| `PINNING_RETRIES` | `3` | Количество попыток пиннинга |
| `PINNING_RETRY_DELAY_MS` | `1000` | Задержка между попытками (мс, умножается на номер попытки) |

## API

Все эндпоинты требуют заголовок `X-API-Key` с валидным ключом.

### Загрузка одного файла

```http
POST /upload
Content-Type: multipart/form-data
X-API-Key: SECRET_KEY_1
```

**Поле формы:** `file`

**Успешный ответ** (`200`):

```json
{
  "cid": "QmXyZ...",
  "name": "document.pdf",
  "size": 245760,
  "pinned": true
}
```

**Ошибка валидации** (`400`):

```json
{
  "error": "Invalid file type",
  "allowedTypes": ["png", "svg", "jpg", "pdf", "doc", "docx", "zip", "json", "html"]
}
```

### Массовая загрузка

```http
POST /upload-multiple
Content-Type: multipart/form-data
X-API-Key: SECRET_KEY_1
```

**Поле формы:** `files` (multiple)

**Успешный ответ** (`200`):

```json
[
  {
    "cid": "QmXyZ...",
    "name": "report.pdf",
    "size": 102400,
    "pinned": true
  },
  {
    "cid": "QmAbC...",
    "name": "logo.png",
    "size": 51200,
    "pinned": true
  }
]
```

### Скачивание файла

```http
GET /file/{cid}
X-API-Key: SECRET_KEY_1
```

Ответ — бинарное содержимое файла с заголовками `Content-Type` и `Content-Disposition: attachment`.

## Аутентификация

Сервис использует аутентификацию по статическим API-ключам через заголовок `X-API-Key`. Ключи задаются в переменной `API_KEYS` через запятую.

Неаутентифицированные запросы получают `401`, запросы с некорректным ключом — `403`.

## Коды ошибок

| Код | Описание |
|---|---|
| `200` | Успех |
| `400` | Ошибка валидации или отсутствует файл |
| `401` | Отсутствует `X-API-Key` |
| `403` | Невалидный `X-API-Key` |
| `404` | Файл не найден в IPFS |
| `413` | Файл превышает `UPLOAD_MAX_FILE_SIZE` |
| `500` | Внутренняя ошибка сервера |

## Зависимости

- [boxo](https://github.com/ipfs/boxo) — файловые абстракции IPFS
- [kubo](https://github.com/ipfs/kubo) — IPFS RPC-клиент

## Лицензия

MIT
