package link

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/1RAFTIK1/linkpulse-link/internal/shortcode"
)

// Repository — хранилище ссылок. Интерфейс объявлен здесь, у потребителя (service),
// а реализация живёт в пакете repository — так тесты подменяют его фейком/моком.
type Repository interface {
	Create(ctx context.Context, l *Link) error
	GetByShortCode(ctx context.Context, code string) (*Link, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]Link, error)
}

// Resolved — минимум данных для редиректа И события клика. link_id обязан
// приезжать даже с кэш-хита (иначе ClickEvent не собрать), поэтому кэшируется
// именно эта пара, а не голый URL.
type Resolved struct {
	LinkID      int64  `json:"id"`
	OriginalURL string `json:"url"`
}

// Cache — кэш соответствия short_code → Resolved (Redis в проде).
type Cache interface {
	Get(ctx context.Context, code string) (res Resolved, found bool, err error)
	Set(ctx context.Context, code string, res Resolved, ttl time.Duration) error
}

// IDGenerator выдаёт уникальные ID (Snowflake).
type IDGenerator interface {
	Next() (int64, error)
}

// Service — бизнес-логика ссылок. Зависимости внедряются через конструктор
// (без DI-фреймворка — идиоматично для Go).
type Service struct {
	repo     Repository
	cache    Cache
	ids      IDGenerator
	baseURL  string
	cacheTTL time.Duration
	now      func() time.Time
	log      *slog.Logger
}

// NewService собирает сервис. now по умолчанию — time.Now (подменяется в тестах).
func NewService(repo Repository, cache Cache, ids IDGenerator, baseURL string, cacheTTL time.Duration, log *slog.Logger) *Service {
	return &Service{
		repo:     repo,
		cache:    cache,
		ids:      ids,
		baseURL:  baseURL,
		cacheTTL: cacheTTL,
		now:      time.Now,
		log:      log,
	}
}

// Create валидирует URL, генерирует Snowflake ID и короткий код, пишет ссылку в
// Postgres и прогревает кэш. Ошибка записи в кэш не проваливает создание —
// источник правды всё равно Postgres, а промах кэша самозалечится при чтении.
func (s *Service) Create(ctx context.Context, userID, rawURL string, expiresAt *time.Time) (*Link, error) {
	normalized, err := NormalizeAndValidateURL(rawURL)
	if err != nil {
		return nil, err // уже обёрнуто ErrInvalidURL
	}

	id, err := s.ids.Next()
	if err != nil {
		return nil, fmt.Errorf("генерация id: %w", err)
	}

	l := &Link{
		ID:          id,
		ShortCode:   shortcode.Encode(id),
		OriginalURL: normalized,
		UserID:      userID,
		CreatedAt:   s.now().UTC(),
		ExpiresAt:   expiresAt,
	}

	if err := s.repo.Create(ctx, l); err != nil {
		return nil, fmt.Errorf("сохранение ссылки: %w", err)
	}

	res := Resolved{LinkID: l.ID, OriginalURL: l.OriginalURL}
	if err := s.cache.Set(ctx, l.ShortCode, res, s.cacheTTLFor(l)); err != nil {
		s.log.WarnContext(ctx, "не удалось прогреть кэш при создании", "short_code", l.ShortCode, "error", err)
	}

	return l, nil
}

// Resolve возвращает данные для редиректа по короткому коду (горячий путь).
//
// Cache-aside: сначала кэш; при промахе идём в Postgres, проверяем срок действия
// и прогреваем кэш. Истёкшая ссылка → ErrExpired, отсутствующая → ErrNotFound.
func (s *Service) Resolve(ctx context.Context, code string) (Resolved, error) {
	if res, found, err := s.cache.Get(ctx, code); err != nil {
		// Сбой кэша не фатален — деградируем к Postgres.
		s.log.WarnContext(ctx, "сбой чтения кэша, идём в БД", "short_code", code, "error", err)
	} else if found {
		return res, nil
	}

	l, err := s.repo.GetByShortCode(ctx, code)
	if err != nil {
		return Resolved{}, err // ErrNotFound или внутренняя ошибка БД
	}
	if l.Expired(s.now()) {
		return Resolved{}, ErrExpired
	}

	res := Resolved{LinkID: l.ID, OriginalURL: l.OriginalURL}
	if err := s.cache.Set(ctx, code, res, s.cacheTTLFor(l)); err != nil {
		s.log.WarnContext(ctx, "не удалось прогреть кэш при чтении", "short_code", code, "error", err)
	}
	return res, nil
}

// ListByUser возвращает ссылки пользователя с пагинацией.
func (s *Service) ListByUser(ctx context.Context, userID string, limit, offset int) ([]Link, error) {
	if limit <= 0 || limit > 100 {
		limit = 20 // дефолт и потолок размера страницы
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListByUser(ctx, userID, limit, offset)
}

// ShortURL собирает полный короткий URL из базового адреса и кода.
func (s *Service) ShortURL(code string) string {
	return s.baseURL + "/" + code
}

// cacheTTLFor не даёт записи в кэше пережить срок действия ссылки: для истекающих
// ссылок TTL кэша не больше времени, оставшегося до истечения.
func (s *Service) cacheTTLFor(l *Link) time.Duration {
	ttl := s.cacheTTL
	if l.ExpiresAt != nil {
		if remaining := l.ExpiresAt.Sub(s.now()); remaining < ttl {
			ttl = remaining
		}
	}
	return ttl
}

// setClock — для тестов: подменяет источник времени.
func (s *Service) setClock(now func() time.Time) { s.now = now }
