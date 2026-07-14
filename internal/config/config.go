// Package config читает конфигурацию сервиса из переменных окружения.
//
// Намеренно без библиотек (viper/envconfig): пара helper'ов на stdlib нагляднее
// и объяснимее. Обязательные переменные проверяются на старте — сервис падает
// сразу с понятной ошибкой, а не спустя время на первом запросе.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config — вся конфигурация Link service.
type Config struct {
	HTTPAddr        string        // адрес HTTP-сервера, напр. ":8080"
	ShutdownTimeout time.Duration // сколько ждём завершения in-flight запросов

	PostgresDSN string // строка подключения к Postgres (обязательна)

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	WorkerID int64  // ID воркера для Snowflake (0..1023)
	BaseURL  string // база для сборки short_url, напр. "http://localhost:8080"

	// KafkaBrokers — пустой срез (KAFKA_BROKERS="") выключает публикацию кликов:
	// сервис остаётся работоспособным без Kafka (например, в юнит-окружении).
	KafkaBrokers []string
	KafkaTopic   string

	IPHashSalt  string // соль sha256(salt+ip); в проде — секрет
	GeoIPDBPath string // путь к GeoLite2-Country.mmdb; "" = страна не резолвится

	// AuthAddr — gRPC-адрес Auth service. Пустой = заглушка авторизации
	// (dev-режим без Auth; сервис громко предупреждает в лог).
	AuthAddr string
}

// Load читает и валидирует конфиг. Возвращает агрегированную ошибку по всем
// проблемным полям сразу, а не по первой попавшейся.
func Load() (Config, error) {
	var errs []error

	cfg := Config{
		HTTPAddr:        getEnv("HTTP_ADDR", ":8080"),
		ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second, &errs),
		PostgresDSN:     os.Getenv("POSTGRES_DSN"),
		RedisAddr:       getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:   os.Getenv("REDIS_PASSWORD"),
		RedisDB:         getEnvInt("REDIS_DB", 0, &errs),
		WorkerID:        int64(getEnvInt("WORKER_ID", 0, &errs)),
		BaseURL:         getEnv("BASE_URL", "http://localhost:8080"),
		KafkaBrokers:    splitNonEmpty(getEnv("KAFKA_BROKERS", "localhost:9092")),
		KafkaTopic:      getEnv("KAFKA_TOPIC", "link-clicks"),
		IPHashSalt:      getEnv("IP_HASH_SALT", "dev-salt-not-secret"),
		GeoIPDBPath:     os.Getenv("GEOIP_DB_PATH"),
		AuthAddr:        os.Getenv("AUTH_ADDR"),
	}

	if cfg.PostgresDSN == "" {
		errs = append(errs, errors.New("POSTGRES_DSN обязателен"))
	}
	if cfg.WorkerID < 0 || cfg.WorkerID > 1023 {
		errs = append(errs, fmt.Errorf("WORKER_ID=%d вне диапазона 0..1023", cfg.WorkerID))
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("конфиг: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// splitNonEmpty режет список по запятым, отбрасывая пустые элементы;
// для "" возвращает nil (признак «выключено»).
func splitNonEmpty(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func getEnvInt(key string, def int, errs *[]error) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: не число (%q)", key, v))
		return def
	}
	return n
}

func getEnvDuration(key string, def time.Duration, errs *[]error) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: не длительность (%q)", key, v))
		return def
	}
	return d
}
