// Package repository — реализация link.Repository поверх Postgres.
//
// Нативный pgx v5 (pgxpool) вместо database/sql: свой пул без лишнего слоя,
// нормальная поддержка типов Postgres. scany/pgxscan снимает бойлерплейт
// rows.Scan — сканирует строки в структуры по db-тегам, SQL остаётся ручным.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1RAFTIK1/linkpulse-link/internal/link"
)

// uniqueViolation — код ошибки Postgres «нарушение уникального ограничения».
const uniqueViolation = "23505"

// linkRow — строка таблицы links. Отдельная от доменной модели структура:
// db-теги и особенности хранения не протекают в домен.
type linkRow struct {
	ID          int64      `db:"id"`
	ShortCode   string     `db:"short_code"`
	OriginalURL string     `db:"original_url"`
	UserID      string     `db:"user_id"`
	CreatedAt   time.Time  `db:"created_at"`
	ExpiresAt   *time.Time `db:"expires_at"`
}

func (r linkRow) toDomain() link.Link {
	return link.Link{
		ID:          r.ID,
		ShortCode:   r.ShortCode,
		OriginalURL: r.OriginalURL,
		UserID:      r.UserID,
		CreatedAt:   r.CreatedAt,
		ExpiresAt:   r.ExpiresAt,
	}
}

// Postgres реализует link.Repository.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres создаёт репозиторий и проверяет соединение (fail-fast на старте).
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("парсинг postgres dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("создание пула: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close закрывает пул (вызывается при graceful shutdown).
func (p *Postgres) Close() { p.pool.Close() }

// Ping — для healthcheck.
func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

// Create вставляет ссылку. Дубликат short_code (уникальный индекс) — внутренняя
// ошибка: Snowflake не должен выдавать коллизии, повтор сигнализирует о неверном
// WORKER_ID у двух инстансов.
func (p *Postgres) Create(ctx context.Context, l *link.Link) error {
	const q = `
		INSERT INTO links (id, short_code, original_url, user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := p.pool.Exec(ctx, q, l.ID, l.ShortCode, l.OriginalURL, l.UserID, l.CreatedAt, l.ExpiresAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return fmt.Errorf("коллизия short_code %q (проверь WORKER_ID инстансов): %w", l.ShortCode, err)
		}
		return fmt.Errorf("insert link: %w", err)
	}
	return nil
}

// GetByShortCode возвращает ссылку по коду; отсутствие строки → link.ErrNotFound.
func (p *Postgres) GetByShortCode(ctx context.Context, code string) (*link.Link, error) {
	const q = `
		SELECT id, short_code, original_url, user_id, created_at, expires_at
		FROM links
		WHERE short_code = $1`

	var row linkRow
	if err := pgxscan.Get(ctx, p.pool, &row, q, code); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, link.ErrNotFound
		}
		return nil, fmt.Errorf("select link by code: %w", err)
	}
	l := row.toDomain()
	return &l, nil
}

// ListByUser возвращает ссылки пользователя, свежие первыми.
func (p *Postgres) ListByUser(ctx context.Context, userID string, limit, offset int) ([]link.Link, error) {
	const q = `
		SELECT id, short_code, original_url, user_id, created_at, expires_at
		FROM links
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`

	var rows []linkRow
	if err := pgxscan.Select(ctx, p.pool, &rows, q, userID, limit, offset); err != nil {
		return nil, fmt.Errorf("select links by user: %w", err)
	}
	links := make([]link.Link, len(rows))
	for i, r := range rows {
		links[i] = r.toDomain()
	}
	return links, nil
}

// Проверка соответствия интерфейсу на этапе компиляции.
var _ link.Repository = (*Postgres)(nil)
