// Package link — доменная логика коротких ссылок: модель, валидация,
// бизнес-сервис и его зависимости (repository, cache) через интерфейсы.
package link

import (
	"errors"
	"time"
)

// Sentinel-ошибки домена. Слой handler по ним выбирает HTTP-статус:
// ErrNotFound → 404, ErrExpired → 410, ErrInvalidURL → 400, остальное → 500.
var (
	ErrNotFound   = errors.New("link: ссылка не найдена")
	ErrExpired    = errors.New("link: срок действия ссылки истёк")
	ErrInvalidURL = errors.New("link: недопустимый URL")
)

// Link — доменная модель ссылки (соответствует строке таблицы links).
type Link struct {
	ID          int64
	ShortCode   string
	OriginalURL string
	UserID      string
	CreatedAt   time.Time
	ExpiresAt   *time.Time // nil = бессрочная
}

// Expired сообщает, истёк ли срок действия ссылки на момент now.
func (l Link) Expired(now time.Time) bool {
	return l.ExpiresAt != nil && now.After(*l.ExpiresAt)
}
