-- Таблица ссылок. id — Snowflake ID, генерируется приложением (не sequence БД):
-- уникальность без координации инстансов, сортировка по id ~ сортировка по времени.
CREATE TABLE links (
    id           BIGINT PRIMARY KEY,
    short_code   TEXT NOT NULL UNIQUE,
    original_url TEXT NOT NULL,
    user_id      TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NULL
);

-- Листинг «мои ссылки»: фильтр по user_id + сортировка по свежести.
-- Составной индекс закрывает запрос ListByUser целиком.
CREATE INDEX idx_links_user_created ON links (user_id, created_at DESC, id DESC);
