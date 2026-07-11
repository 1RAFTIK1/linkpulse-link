package link

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/1RAFTIK1/linkpulse-link/internal/shortcode"
)

// ── Фейки зависимостей ────────────────────────────────────────────────────────

type fakeRepo struct {
	byCode    map[string]*Link
	createErr error
	getErr    error
	created   []*Link
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byCode: map[string]*Link{}} }

func (r *fakeRepo) Create(_ context.Context, l *Link) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.byCode[l.ShortCode] = l
	r.created = append(r.created, l)
	return nil
}

func (r *fakeRepo) GetByShortCode(_ context.Context, code string) (*Link, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	l, ok := r.byCode[code]
	if !ok {
		return nil, ErrNotFound
	}
	return l, nil
}

func (r *fakeRepo) ListByUser(_ context.Context, _ string, _, _ int) ([]Link, error) {
	return nil, nil
}

type fakeCache struct {
	data    map[string]Resolved
	setErr  error
	getErr  error
	gets    int
	lastTTL time.Duration
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string]Resolved{}} }

func (c *fakeCache) Get(_ context.Context, code string) (Resolved, bool, error) {
	c.gets++
	if c.getErr != nil {
		return Resolved{}, false, c.getErr
	}
	res, ok := c.data[code]
	return res, ok, nil
}

func (c *fakeCache) Set(_ context.Context, code string, res Resolved, ttl time.Duration) error {
	if c.setErr != nil {
		return c.setErr
	}
	c.data[code] = res
	c.lastTTL = ttl
	return nil
}

type fakeIDs struct{ id int64 }

func (f *fakeIDs) Next() (int64, error) { return f.id, nil }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newService(repo Repository, cache Cache, id int64) *Service {
	return NewService(repo, cache, &fakeIDs{id: id}, "http://short.test", time.Hour, discardLogger())
}

// ── Тесты ─────────────────────────────────────────────────────────────────────

func TestCreate_StoresAndWarmsCache(t *testing.T) {
	repo, cache := newFakeRepo(), newFakeCache()
	svc := newService(repo, cache, 123456789)

	l, err := svc.Create(context.Background(), "user-1", "https://example.com/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.ShortCode != shortcode.Encode(123456789) {
		t.Errorf("short_code=%q, want %q", l.ShortCode, shortcode.Encode(123456789))
	}
	if repo.byCode[l.ShortCode] == nil {
		t.Error("ссылка не записана в repo")
	}
	want := Resolved{LinkID: 123456789, OriginalURL: "https://example.com/a"}
	if cache.data[l.ShortCode] != want {
		t.Errorf("кэш не прогрет при создании: %+v", cache.data[l.ShortCode])
	}
}

func TestCreate_InvalidURL(t *testing.T) {
	repo, cache := newFakeRepo(), newFakeCache()
	svc := newService(repo, cache, 1)

	_, err := svc.Create(context.Background(), "user-1", "not-a-url", nil)
	if !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("ожидали ErrInvalidURL, got %v", err)
	}
	if len(repo.created) != 0 {
		t.Error("при невалидном URL ничего не должно записываться")
	}
}

func TestCreate_CacheFailureDoesNotFail(t *testing.T) {
	repo, cache := newFakeRepo(), newFakeCache()
	cache.setErr = errors.New("redis down")
	svc := newService(repo, cache, 1)

	if _, err := svc.Create(context.Background(), "u", "https://example.com", nil); err != nil {
		t.Fatalf("сбой кэша не должен проваливать создание, got %v", err)
	}
	if len(repo.created) != 1 {
		t.Error("ссылка должна остаться в БД несмотря на сбой кэша")
	}
}

func TestResolve_CacheHitSkipsRepo(t *testing.T) {
	repo, cache := newFakeRepo(), newFakeCache()
	cache.data["abc"] = Resolved{LinkID: 9, OriginalURL: "https://cached.example"}
	repo.getErr = errors.New("repo не должен вызываться") // взорвётся, если дойдём до БД
	svc := newService(repo, cache, 1)

	res, err := svc.Resolve(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if res.OriginalURL != "https://cached.example" || res.LinkID != 9 {
		t.Errorf("res=%+v", res)
	}
}

func TestResolve_CacheMissWarmsFromRepo(t *testing.T) {
	repo, cache := newFakeRepo(), newFakeCache()
	repo.byCode["abc"] = &Link{ID: 5, ShortCode: "abc", OriginalURL: "https://db.example"}
	svc := newService(repo, cache, 1)

	res, err := svc.Resolve(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if res.OriginalURL != "https://db.example" || res.LinkID != 5 {
		t.Errorf("res=%+v", res)
	}
	want := Resolved{LinkID: 5, OriginalURL: "https://db.example"}
	if cache.data["abc"] != want {
		t.Error("после промаха кэш должен быть прогрет из БД (включая link_id)")
	}
}

func TestResolve_NotFound(t *testing.T) {
	svc := newService(newFakeRepo(), newFakeCache(), 1)
	if _, err := svc.Resolve(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидали ErrNotFound, got %v", err)
	}
}

func TestResolve_Expired(t *testing.T) {
	repo, cache := newFakeRepo(), newFakeCache()
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	repo.byCode["abc"] = &Link{ShortCode: "abc", OriginalURL: "https://x", ExpiresAt: &past}
	svc := newService(repo, cache, 1)

	if _, err := svc.Resolve(context.Background(), "abc"); !errors.Is(err, ErrExpired) {
		t.Fatalf("ожидали ErrExpired, got %v", err)
	}
}

func TestResolve_CacheTTLCappedByExpiry(t *testing.T) {
	repo, cache := newFakeRepo(), newFakeCache()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	exp := now.Add(5 * time.Minute) // истекает раньше, чем cacheTTL (1 час)
	repo.byCode["abc"] = &Link{ShortCode: "abc", OriginalURL: "https://x", ExpiresAt: &exp}
	svc := newService(repo, cache, 1)
	svc.setClock(func() time.Time { return now })

	if _, err := svc.Resolve(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	if cache.lastTTL != 5*time.Minute {
		t.Errorf("TTL кэша=%v, ожидали 5m (ограничен сроком действия)", cache.lastTTL)
	}
}
