package snowflake

import (
	"sync"
	"testing"
	"time"
)

// manualClock — управляемый источник времени для детерминированных тестов.
type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestNew_WorkerIDValidation(t *testing.T) {
	tests := []struct {
		name     string
		workerID int64
		wantErr  bool
	}{
		{"ноль", 0, false},
		{"максимум", maxWorkerID, false},
		{"отрицательный", -1, true},
		{"выше максимума", maxWorkerID + 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.workerID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%d): err=%v, wantErr=%v", tt.workerID, err, tt.wantErr)
			}
		})
	}
}

func TestNext_PositiveAndDecodesWorkerAndTime(t *testing.T) {
	clk := &manualClock{t: base}
	g, err := New(42, WithClock(clk.now))
	if err != nil {
		t.Fatal(err)
	}

	id, err := g.Next()
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("ID должен быть положительным, получили %d", id)
	}

	parts := Decompose(id)
	if parts.WorkerID != 42 {
		t.Errorf("worker id: got %d, want 42", parts.WorkerID)
	}
	if parts.Sequence != 0 {
		t.Errorf("первый sequence в миллисекунде должен быть 0, got %d", parts.Sequence)
	}
	if parts.TimestampMs != base.UnixMilli() {
		t.Errorf("timestamp: got %d, want %d", parts.TimestampMs, base.UnixMilli())
	}
}

func TestNext_SequenceIncrementsWithinSameMillis(t *testing.T) {
	clk := &manualClock{t: base}
	g, _ := New(1, WithClock(clk.now))

	// 4096 ID в одной и той же миллисекунде: sequence обязан пройти 0..4095 без повторов.
	const n = maxSequence + 1
	seen := make(map[int64]struct{}, n)
	var prev int64
	for i := 0; i < n; i++ {
		id, err := g.Next()
		if err != nil {
			t.Fatalf("вызов %d: %v", i, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("вызов %d: дубликат ID %d", i, id)
		}
		seen[id] = struct{}{}
		if i > 0 && id <= prev {
			t.Fatalf("вызов %d: ID не растёт монотонно (%d <= %d)", i, id, prev)
		}
		prev = id
		if got := Decompose(id).Sequence; got != int64(i) {
			t.Fatalf("вызов %d: sequence=%d, want %d", i, got, i)
		}
	}
}

func TestNext_SequenceOverflowWaitsForNextMillis(t *testing.T) {
	clk := &manualClock{t: base}
	g, _ := New(1, WithClock(clk.now))

	// Исчерпываем sequence текущей миллисекунды.
	for i := 0; i <= maxSequence; i++ {
		if _, err := g.Next(); err != nil {
			t.Fatal(err)
		}
	}

	// Следующий вызов обязан заблокироваться в ожидании новой миллисекунды.
	done := make(chan int64, 1)
	go func() {
		id, err := g.Next()
		if err != nil {
			t.Errorf("Next после переполнения: %v", err)
		}
		done <- id
	}()

	select {
	case id := <-done:
		t.Fatalf("Next не должен был вернуться при неподвижных часах, вернул %d", id)
	case <-time.After(50 * time.Millisecond):
		// ожидаемо: горутина крутится в waitNextMillis
	}

	// Сдвигаем часы вперёд — вызов должен разблокироваться в новой миллисекунде.
	clk.set(base.Add(time.Millisecond))
	select {
	case id := <-done:
		parts := Decompose(id)
		if parts.Sequence != 0 {
			t.Errorf("после перехода в новую мс sequence=%d, want 0", parts.Sequence)
		}
		if parts.TimestampMs != base.Add(time.Millisecond).UnixMilli() {
			t.Errorf("timestamp не тот: got %d", parts.TimestampMs)
		}
	case <-time.After(time.Second):
		t.Fatal("Next не разблокировался после сдвига часов вперёд")
	}
}

func TestNext_ClockMovedBackwards(t *testing.T) {
	clk := &manualClock{t: base}
	g, _ := New(1, WithClock(clk.now))

	if _, err := g.Next(); err != nil {
		t.Fatal(err)
	}

	clk.set(base.Add(-5 * time.Millisecond))
	if _, err := g.Next(); err == nil {
		t.Fatal("ожидали ошибку при обратном ходе часов, получили nil")
	}
}

func TestNext_ConcurrentUniqueness(t *testing.T) {
	g, _ := New(7) // реальные часы
	const goroutines = 16
	const perGoroutine = 5000

	var mu sync.Mutex
	seen := make(map[int64]struct{}, goroutines*perGoroutine)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				id, err := g.Next()
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				mu.Lock()
				if _, dup := seen[id]; dup {
					t.Errorf("дубликат ID %d", id)
				}
				seen[id] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("уникальных ID %d, ожидали %d", len(seen), goroutines*perGoroutine)
	}
}
