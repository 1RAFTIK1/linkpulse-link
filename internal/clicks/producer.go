// Package clicks — сборка и публикация событий клика в Kafka.
//
// Требование спеки: редирект НЕ ждёт Kafka. franz-go это даёт из коробки:
// kgo.Produce кладёт запись во внутренний буфер и возвращается сразу, доставку
// ведёт фоновая горутина клиента (батчинг, ретраи, идемпотентный продюсер).
//
// Семантика на этом этапе — at-most-once: если доставка не удалась после
// ретраев, событие теряется (пишем в лог). Гарантированная доставка через
// transactional outbox — осознанная stretch-цель (спека §6), не MVP.
package clicks

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/1RAFTIK1/linkpulse-contracts/gen/go/events/v1"
)

// Producer публикует ClickEvent в Kafka.
type Producer struct {
	client *kgo.Client
	log    *slog.Logger
}

// NewProducer создаёт продюсер и проверяет доступность кластера (fail-fast).
func NewProducer(ctx context.Context, brokers []string, topic string, log *slog.Logger) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		// Сжатие батчей; идемпотентный продюсер и acks=all у kgo включены по умолчанию.
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka client: %w", err)
	}
	if err := client.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("kafka ping: %w", err)
	}
	return &Producer{client: client, log: log}, nil
}

// Publish асинхронно отправляет событие. Возвращается немедленно — вызывается
// на пути редиректа. Ключ партиции — short_code: все события одной ссылки
// попадают в одну партицию, значит порядок для ссылки сохраняется (спека §6).
//
// context.Background() намеренно: жизнь события не привязана к HTTP-запросу —
// редирект уже отправлен клиенту, отмена запроса не должна отменять доставку.
func (p *Producer) Publish(ev *eventsv1.ClickEvent) {
	payload, err := proto.Marshal(ev)
	if err != nil {
		p.log.Error("clicks: marshal события", "error", err, "event_id", ev.GetEventId())
		return
	}

	rec := &kgo.Record{
		Key:   []byte(ev.GetShortCode()),
		Value: payload,
	}
	p.client.Produce(context.Background(), rec, func(r *kgo.Record, err error) {
		if err != nil {
			// at-most-once: после исчерпания ретраев событие теряется — фиксируем в лог.
			p.log.Error("clicks: событие не доставлено",
				"error", err, "event_id", ev.GetEventId(), "short_code", ev.GetShortCode())
		}
	})
}

// Close дожидается доставки всего буфера (вызывается при graceful shutdown
// ПОСЛЕ остановки HTTP-сервера: новых кликов уже нет, добиваем хвост).
func (p *Producer) Close(ctx context.Context) error {
	defer p.client.Close()
	if err := p.client.Flush(ctx); err != nil {
		return fmt.Errorf("kafka flush: %w", err)
	}
	return nil
}
