// Package cache — реализация link.Cache поверх Redis (go-redis v9).
//
// Ключи: link:{short_code} → original_url. Простые строки с TTL — по схеме
// из спеки; сервис-слой сам решает, что промах/сбой кэша не фатален.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/1RAFTIK1/linkpulse-link/internal/link"
)

const keyPrefix = "link:"

// Redis реализует link.Cache.
type Redis struct {
	client *redis.Client
}

// NewRedis подключается к Redis и проверяет соединение (fail-fast на старте).
func NewRedis(ctx context.Context, addr, password string, db int) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Redis{client: client}, nil
}

// Close закрывает соединения (graceful shutdown).
func (r *Redis) Close() error { return r.client.Close() }

// Ping — для healthcheck.
func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

// Get читает запись из кэша. Отсутствие ключа — не ошибка, а промах.
// Значение — JSON {"id":...,"url":...}; неразбираемое значение (например,
// оставшееся от старого формата) тоже считаем промахом: чтение из БД
// перезапишет ключ корректно — кэш самозалечивается.
func (r *Redis) Get(ctx context.Context, code string) (link.Resolved, bool, error) {
	val, err := r.client.Get(ctx, keyPrefix+code).Bytes()
	if errors.Is(err, redis.Nil) {
		return link.Resolved{}, false, nil
	}
	if err != nil {
		return link.Resolved{}, false, fmt.Errorf("redis get: %w", err)
	}
	var res link.Resolved
	if err := json.Unmarshal(val, &res); err != nil || res.LinkID == 0 || res.OriginalURL == "" {
		return link.Resolved{}, false, nil // битое/устаревшее значение = промах
	}
	return res, true, nil
}

// Set пишет запись в кэш с TTL. Неположительный TTL не пишем вовсе:
// ссылка уже истекла или на грани — кэшировать нечего.
func (r *Redis) Set(ctx context.Context, code string, res link.Resolved, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	payload, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal cache value: %w", err)
	}
	if err := r.client.Set(ctx, keyPrefix+code, payload, ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}

var _ link.Cache = (*Redis)(nil)
