# linkpulse-link

Link service проекта **LinkPulse**: REST API создания коротких ссылок, редирект
по ним и (с фазы 3) публикация событий клика в Kafka.

## Архитектура

Слои `handler → service → repository`, зависимости через конструкторы:

```
cmd/link/main.go            — сборка зависимостей, graceful shutdown
internal/
  config/                   — конфиг из env (stdlib, fail-fast валидация)
  snowflake/                — генератор ID: 41 бит время / 10 worker / 12 sequence
  shortcode/                — base62-кодирование ID → короткий код
  link/                     — домен: модель, sentinel-ошибки, валидация URL, service
  repository/               — link.Repository на pgx v5 + scany (нативный pgx, без database/sql)
  httpapi/                  — роутер (stdlib mux, Go 1.22+ паттерны), middleware, handlers
migrations/                 — golang-migrate (up/down пары)
```

Ключевые решения:
- **Snowflake ID** генерируется приложением: уникальность без координации,
  ID монотонно растут, `WORKER_ID` различает инстансы. Короткий код = base62(id).
- **Cache-aside** в Redis (`link:{code}` → url): промах → Postgres → прогрев.
  Сбой Redis не роняет запрос — деградация к БД. TTL кэша обрезается `expires_at`.
- **Ошибки домена** — sentinel (`ErrNotFound`, `ErrExpired`, `ErrInvalidURL`),
  handler переводит их в 404 / 410 / 400; внутренние детали клиенту не отдаются.
- **Валидация URL**: только http/https, отсекаются localhost и приватные/loopback
  IP — шортенер нельзя использовать для маскировки ссылок на внутренние ресурсы.
- Редирект — **302**, не 301: постоянный редирект кэшируется браузером навсегда,
  что убило бы и аналитику кликов, и истечение ссылок.
- **Авторизация — заглушка фазы 2** (`stub-user`): по плану сборки (SPEC §13)
  реальный JWT появится в фазе 5, чтобы не блокироваться на Auth service.

## API

| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/v1/links` | создать ссылку: `{"original_url": "...", "expires_at": "..."}`(опц.) |
| `GET` | `/api/v1/links?limit=&offset=` | ссылки текущего пользователя |
| `GET` | `/{code}` | 302-редирект (404 — нет, 410 — истекла) |
| `GET` | `/healthz` | здоровье зависимостей (200/503) |

## Конфигурация (env)

| Переменная | Дефолт | Описание |
|---|---|---|
| `POSTGRES_DSN` | — (обязательна) | `postgres://user:pass@host:5432/linkpulse_link` |
| `REDIS_ADDR` | `localhost:6379` | адрес Redis |
| `REDIS_PASSWORD` | `""` | пароль Redis |
| `REDIS_DB` | `0` | номер БД Redis |
| `HTTP_ADDR` | `:8080` | адрес HTTP-сервера |
| `BASE_URL` | `http://localhost:8080` | база для short_url |
| `WORKER_ID` | `0` | ID воркера Snowflake (0..1023, уникален на инстанс) |
| `SHUTDOWN_TIMEOUT` | `10s` | дренаж in-flight запросов при остановке |
| `KAFKA_BROKERS` | `localhost:9092` | брокеры через запятую; `""` выключает публикацию кликов |
| `KAFKA_TOPIC` | `link-clicks` | топик событий клика |
| `IP_HASH_SALT` | `dev-salt-not-secret` | соль sha256(salt+ip); в проде — секрет |
| `GEOIP_DB_PATH` | `""` | путь к GeoLite2-Country.mmdb; пусто = страна не резолвится |

## Запуск локально

```bash
# 1) Поднять Postgres+Redis из соседнего репозитория
cd ../linkpulse-infra && make up

# 2) Накатить миграции (одноразово: make tools ставит migrate CLI)
make tools && make migrate-up

# 3) Запустить
make run
```

## Зависимости и версии

| Библиотека | Версия | Роль |
|---|---|---|
| jackc/pgx/v5 | 5.10.0 | нативный драйвер Postgres + пул |
| georgysavva/scany/v2 | 2.1.4 | сканирование строк в структуры (sqlx-эргономика на pgx) |
| redis/go-redis/v9 | 9.21.0 | клиент Redis |
| twmb/franz-go | 1.21.5 | Kafka-продюсер (асинхронный, идемпотентный, acks=all) |
| oschwald/geoip2-golang/v2 | 2.2.0 | оффлайн-резолв страны по IP (MaxMind GeoLite2) |
| golang-migrate/v4 | 4.19.1 | миграции схемы (CLI) |

Go 1.26, роутинг — stdlib `net/http`.

## Тесты

```bash
make test    # юнит-тесты с -race
make cover   # с отчётом покрытия
```

Snowflake, base62, валидация URL и service-слой покрыты юнит-тестами на фейках;
integration-тесты через testcontainers-go добавляются в фазе 6 (SPEC §12).
