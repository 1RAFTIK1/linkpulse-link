package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/1RAFTIK1/linkpulse-link/internal/link"
)

// Фейки повторяют минимально нужное поведение зависимостей service-слоя —
// HTTP-тесты гоняют весь путь router → middleware → handler → service.

type memRepo struct{ byCode map[string]*link.Link }

func (r *memRepo) Create(_ context.Context, l *link.Link) error {
	r.byCode[l.ShortCode] = l
	return nil
}

func (r *memRepo) GetByShortCode(_ context.Context, code string) (*link.Link, error) {
	l, ok := r.byCode[code]
	if !ok {
		return nil, link.ErrNotFound
	}
	return l, nil
}

func (r *memRepo) ListByUser(_ context.Context, userID string, _, _ int) ([]link.Link, error) {
	var out []link.Link
	for _, l := range r.byCode {
		if l.UserID == userID {
			out = append(out, *l)
		}
	}
	return out, nil
}

type memCache struct{ data map[string]link.Resolved }

func (c *memCache) Get(_ context.Context, code string) (link.Resolved, bool, error) {
	res, ok := c.data[code]
	return res, ok, nil
}

func (c *memCache) Set(_ context.Context, code string, res link.Resolved, _ time.Duration) error {
	c.data[code] = res
	return nil
}

type seqIDs struct{ next int64 }

func (s *seqIDs) Next() (int64, error) {
	s.next++
	return s.next, nil
}

func newTestServer(t *testing.T) (*httptest.Server, *memRepo) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := &memRepo{byCode: map[string]*link.Link{}}
	svc := link.NewService(repo, &memCache{data: map[string]link.Resolved{}}, &seqIDs{}, "http://short.test", time.Hour, log)
	// builder/publisher = nil: клики в этих тестах не публикуются.
	router := NewRouter(NewHandlers(svc, nil, nil, log), log, nil, "test-user")
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, repo
}

func TestCreateAndRedirect_FullFlow(t *testing.T) {
	srv, _ := newTestServer(t)

	// Создание ссылки.
	resp, err := http.Post(srv.URL+"/api/v1/links", "application/json",
		strings.NewReader(`{"original_url":"https://example.com/target"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: статус %d, want 201", resp.StatusCode)
	}
	var created struct {
		ShortCode string `json:"short_code"`
		ShortURL  string `json:"short_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ShortCode == "" || !strings.HasSuffix(created.ShortURL, "/"+created.ShortCode) {
		t.Fatalf("некорректный ответ создания: %+v", created)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("нет X-Request-Id в ответе")
	}

	// Редирект: без следования за ним — проверяем сам 302 и Location.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	redir, err := client.Get(srv.URL + "/" + created.ShortCode)
	if err != nil {
		t.Fatal(err)
	}
	defer redir.Body.Close()
	if redir.StatusCode != http.StatusFound {
		t.Fatalf("redirect: статус %d, want 302", redir.StatusCode)
	}
	if loc := redir.Header.Get("Location"); loc != "https://example.com/target" {
		t.Fatalf("Location=%q", loc)
	}
}

func TestCreate_BadRequests(t *testing.T) {
	srv, _ := newTestServer(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"мусорный json", `{not json`, http.StatusBadRequest},
		{"невалидный url", `{"original_url":"javascript:alert(1)"}`, http.StatusBadRequest},
		{"внутренний хост", `{"original_url":"http://localhost/admin"}`, http.StatusBadRequest},
		{"expires_at в прошлом", `{"original_url":"https://example.com","expires_at":"2020-01-01T00:00:00Z"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/api/v1/links", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("статус %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestRedirect_NotFoundAndGone(t *testing.T) {
	srv, repo := newTestServer(t)

	past := time.Now().Add(-time.Hour)
	repo.byCode["dead"] = &link.Link{ShortCode: "dead", OriginalURL: "https://x", ExpiresAt: &past}

	for _, tt := range []struct {
		code string
		want int
	}{
		{"nope", http.StatusNotFound},
		{"dead", http.StatusGone},
	} {
		resp, err := http.Get(srv.URL + "/" + tt.code)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Fatalf("/%s: статус %d, want %d", tt.code, resp.StatusCode, tt.want)
		}
	}
}

func TestListLinks_OnlyCurrentUser(t *testing.T) {
	srv, repo := newTestServer(t)

	repo.byCode["a"] = &link.Link{ShortCode: "a", OriginalURL: "https://a", UserID: "test-user"}
	repo.byCode["b"] = &link.Link{ShortCode: "b", OriginalURL: "https://b", UserID: "someone-else"}

	resp, err := http.Get(srv.URL + "/api/v1/links")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d", resp.StatusCode)
	}
	var links []struct {
		ShortCode string `json:"short_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&links); err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].ShortCode != "a" {
		t.Fatalf("ожидали только ссылку 'a' пользователя test-user, получили %+v", links)
	}
}
