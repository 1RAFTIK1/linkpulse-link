// Package snowflake генерирует 63-битные, монотонно растущие, уникальные ID
// без координации между инстансами (Twitter Snowflake).
//
// Раскладка 63 бит (знаковый бит int64 не используется — ID всегда положительный):
//
//		 63           22           12            0
//		┌─┬───────────────────────┬──────────┬──────────┐
//		│0│  41 бит: время (мс)    │10: worker│12: seq   │
//		└─┴───────────────────────┴──────────┴──────────┘
//
//	  - 41 бит времени в миллисекундах от кастомной эпохи → ~69 лет диапазона;
//	  - 10 бит worker ID → до 1024 инстансов одновременно;
//	  - 12 бит sequence → до 4096 ID на один воркер в одну миллисекунду.
package snowflake

import (
	"fmt"
	"sync"
	"time"
)

const (
	workerBits   = 10
	sequenceBits = 12

	maxWorkerID = -1 ^ (-1 << workerBits)   // 1023
	maxSequence = -1 ^ (-1 << sequenceBits) // 4095

	workerShift    = sequenceBits              // sequence занимает младшие биты
	timestampShift = sequenceBits + workerBits // 22
)

// epoch — кастомная точка отсчёта: 2024-01-01T00:00:00Z в миллисекундах Unix.
// Своя эпоха вместо Unix-эпохи экономит биты времени (счёт идёт от 2024, а не 1970).
var epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

// Generator потокобезопасно выдаёт Snowflake ID. Создаётся через New.
type Generator struct {
	mu       sync.Mutex
	workerID int64
	lastMs   int64 // время последнего выданного ID
	sequence int64 // счётчик в пределах одной миллисекунды

	now func() time.Time // источник времени, подменяется в тестах
}

// Option настраивает Generator.
type Option func(*Generator)

// WithClock подменяет источник времени (для тестов).
func WithClock(now func() time.Time) Option {
	return func(g *Generator) { g.now = now }
}

// New создаёт генератор для заданного worker ID (0..1023).
func New(workerID int64, opts ...Option) (*Generator, error) {
	if workerID < 0 || workerID > maxWorkerID {
		return nil, fmt.Errorf("snowflake: worker id %d вне диапазона 0..%d", workerID, maxWorkerID)
	}
	g := &Generator{workerID: workerID, now: time.Now}
	for _, opt := range opts {
		opt(g)
	}
	return g, nil
}

// Next возвращает следующий уникальный ID.
//
// Ошибку возвращает только при обратном ходе системных часов: выдавать ID «из прошлого»
// нельзя — это ломает монотонность и грозит коллизиями. Вызывающий код в таком случае
// отвечает 5xx (лучше отказать в создании ссылки, чем выдать неуникальный ID).
func (g *Generator) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	nowMs := g.now().UnixMilli()

	if nowMs < g.lastMs {
		return 0, fmt.Errorf("snowflake: часы ушли назад на %d мс", g.lastMs-nowMs)
	}

	if nowMs == g.lastMs {
		// Та же миллисекунда — увеличиваем sequence.
		g.sequence = (g.sequence + 1) & maxSequence
		if g.sequence == 0 {
			// sequence исчерпан в этой мс — ждём наступления следующей.
			nowMs = g.waitNextMillis(nowMs)
		}
	} else {
		// Новая миллисекунда — сбрасываем sequence.
		g.sequence = 0
	}

	g.lastMs = nowMs

	id := ((nowMs - epoch) << timestampShift) |
		(g.workerID << workerShift) |
		g.sequence
	return id, nil
}

// waitNextMillis активно ждёт наступления следующей миллисекунды.
func (g *Generator) waitNextMillis(nowMs int64) int64 {
	for nowMs <= g.lastMs {
		nowMs = g.now().UnixMilli()
	}
	return nowMs
}

// Parts — разобранный на составляющие ID (для тестов и отладки).
type Parts struct {
	TimestampMs int64 // абсолютное время в мс Unix (эпоха уже прибавлена обратно)
	WorkerID    int64
	Sequence    int64
}

// Decompose раскладывает ID обратно на время/воркер/sequence.
func Decompose(id int64) Parts {
	return Parts{
		TimestampMs: (id >> timestampShift) + epoch,
		WorkerID:    (id >> workerShift) & maxWorkerID,
		Sequence:    id & maxSequence,
	}
}
