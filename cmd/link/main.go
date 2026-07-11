// Link service — REST API коротких ссылок: создание, листинг, редирект.
//
// Сборка зависимостей — вручную через конструкторы (без DI-фреймворка):
// config → postgres/redis/snowflake → service → handlers → router → server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/1RAFTIK1/linkpulse-link/internal/cache"
	"github.com/1RAFTIK1/linkpulse-link/internal/clicks"
	"github.com/1RAFTIK1/linkpulse-link/internal/config"
	"github.com/1RAFTIK1/linkpulse-link/internal/httpapi"
	"github.com/1RAFTIK1/linkpulse-link/internal/link"
	"github.com/1RAFTIK1/linkpulse-link/internal/repository"
	"github.com/1RAFTIK1/linkpulse-link/internal/snowflake"
)

// cacheTTL — время жизни записи link:{code} в Redis. Час — компромисс:
// достаточно долго для горячих ссылок, достаточно коротко для памяти Redis.
const cacheTTL = time.Hour

// stubUserID — фаза 2: авторизация — заглушка (спека §13). Все запросы идут
// от имени этого пользователя, пока в фазе 5 не появится настоящий JWT.
const stubUserID = "stub-user"

func main() {
	// JSON-логи в stdout — их собирает Docker/агрегатор (спека §10).
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("сервис завершился с ошибкой", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// Контекст жизни процесса: отменяется по SIGINT/SIGTERM — от него зависят
	// и инициализация (не зависаем на недоступной БД), и начало shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Зависимости: fail-fast — если Postgres/Redis недоступны, падаем на старте
	// с внятной ошибкой, а не на первом запросе.
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	repo, err := repository.NewPostgres(initCtx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer repo.Close()

	rds, err := cache.NewRedis(initCtx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() {
		if err := rds.Close(); err != nil {
			log.Warn("закрытие redis", "error", err)
		}
	}()

	ids, err := snowflake.New(cfg.WorkerID)
	if err != nil {
		return err
	}

	// Публикация кликов: включена, если задан KAFKA_BROKERS.
	var (
		producer *clicks.Producer
		builder  *clicks.Builder
	)
	if len(cfg.KafkaBrokers) > 0 {
		producer, err = clicks.NewProducer(initCtx, cfg.KafkaBrokers, cfg.KafkaTopic, log)
		if err != nil {
			return err
		}

		// GeoIP опционален: без базы страна остаётся пустой, пайплайн работает.
		var geo clicks.CountryResolver = clicks.NoopResolver{}
		if cfg.GeoIPDBPath != "" {
			g, err := clicks.NewGeoIP(cfg.GeoIPDBPath)
			if err != nil {
				return err
			}
			defer func() {
				if err := g.Close(); err != nil {
					log.Warn("закрытие geoip", "error", err)
				}
			}()
			geo = g
			log.Info("geoip включён", "db", cfg.GeoIPDBPath)
		}
		builder = clicks.NewBuilder(ids, cfg.IPHashSalt, geo)
		log.Info("публикация кликов включена", "brokers", cfg.KafkaBrokers, "topic", cfg.KafkaTopic)
	} else {
		log.Warn("KAFKA_BROKERS пуст — клики НЕ публикуются")
	}

	svc := link.NewService(repo, rds, ids, cfg.BaseURL, cacheTTL, log)

	// producer в интерфейс кладём только не-nil: typed-nil указатель внутри
	// интерфейса не равен nil, и проверка h.pub != nil в handler'е обманулась бы.
	var pub httpapi.ClickPublisher
	if producer != nil {
		pub = producer
	}
	handlers := httpapi.NewHandlers(svc, builder, pub, log)
	router := httpapi.NewRouter(handlers, log, map[string]httpapi.Pinger{
		"postgres": repo,
		"redis":    rds,
	}, stubUserID)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second, // защита от slowloris
	}

	// Сервер — в горутине; главная горутина ждёт сигнала или ошибки сервера.
	errCh := make(chan error, 1)
	go func() {
		log.Info("http сервер запущен", "addr", cfg.HTTPAddr, "worker_id", cfg.WorkerID)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// flushProducer дожидается доставки буферизованных кликов. Вызывается
	// ПОСЛЕ остановки HTTP (новые клики уже не приходят) и ДО закрытия пулов.
	flushProducer := func() {
		if producer == nil {
			return
		}
		flushCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := producer.Close(flushCtx); err != nil {
			log.Error("flush kafka-продюсера", "error", err)
		} else {
			log.Info("kafka-продюсер: буфер доставлен")
		}
	}

	select {
	case err := <-errCh:
		flushProducer()
		return err
	case <-ctx.Done():
		// Graceful shutdown: перестаём принимать новые запросы, in-flight
		// дорабатывают до ShutdownTimeout, потом принудительно.
		log.Info("получен сигнал, останавливаемся", "timeout", cfg.ShutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		flushProducer()
		// Пулы Postgres/Redis закроются defer'ами выше.
		log.Info("сервис остановлен корректно")
		return nil
	}
}
