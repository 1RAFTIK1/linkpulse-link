// Package httpapi — HTTP-слой Link service: роутер, middleware, handlers.
// Ошибки домена (sentinel из пакета link) транслируются в HTTP-статусы здесь —
// service ничего не знает про HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	eventsv1 "github.com/1RAFTIK1/linkpulse-contracts/gen/go/events/v1"

	"github.com/1RAFTIK1/linkpulse-link/internal/clicks"
	"github.com/1RAFTIK1/linkpulse-link/internal/link"
)

// ClickPublisher асинхронно публикует событие клика (Kafka в проде).
// Интерфейс объявлен у потребителя; nil = публикация выключена (тесты,
// запуск без Kafka).
type ClickPublisher interface {
	Publish(ev *eventsv1.ClickEvent)
}

// Handlers держит зависимости HTTP-слоя.
type Handlers struct {
	svc     *link.Service
	builder *clicks.Builder
	pub     ClickPublisher
	log     *slog.Logger
}

// NewHandlers: builder и pub могут быть nil — тогда клики не публикуются.
func NewHandlers(svc *link.Service, builder *clicks.Builder, pub ClickPublisher, log *slog.Logger) *Handlers {
	return &Handlers{svc: svc, builder: builder, pub: pub, log: log}
}

// ── DTO ───────────────────────────────────────────────────────────────────────

type createLinkRequest struct {
	OriginalURL string     `json:"original_url"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"` // RFC 3339, опционально
}

type linkResponse struct {
	ShortCode   string     `json:"short_code"`
	ShortURL    string     `json:"short_url"`
	OriginalURL string     `json:"original_url"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handlers) toResponse(l *link.Link) linkResponse {
	return linkResponse{
		ShortCode:   l.ShortCode,
		ShortURL:    h.svc.ShortURL(l.ShortCode),
		OriginalURL: l.OriginalURL,
		CreatedAt:   l.CreatedAt,
		ExpiresAt:   l.ExpiresAt,
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// CreateLink — POST /api/v1/links.
func (h *Handlers) CreateLink(w http.ResponseWriter, r *http.Request) {
	var req createLinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "невалидный JSON")
		return
	}

	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at в прошлом")
		return
	}

	l, err := h.svc.Create(r.Context(), UserIDFrom(r.Context()), req.OriginalURL, req.ExpiresAt)
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.toResponse(l))
}

// ListLinks — GET /api/v1/links (ссылки текущего пользователя).
func (h *Handlers) ListLinks(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	offset := queryInt(r, "offset", 0)

	links, err := h.svc.ListByUser(r.Context(), UserIDFrom(r.Context()), limit, offset)
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}

	resp := make([]linkResponse, len(links))
	for i := range links {
		resp[i] = h.toResponse(&links[i])
	}
	writeJSON(w, http.StatusOK, resp)
}

// Redirect — GET /{code}: 302 на original_url. Горячий путь: сначала Redis,
// на промахе Postgres.
func (h *Handlers) Redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	res, err := h.svc.Resolve(r.Context(), code)
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}

	// 302 Found, не 301: постоянный редирект браузеры кэшируют навсегда,
	// и мы потеряли бы и клики (аналитика!), и возможность истечения ссылки.
	http.Redirect(w, r, res.OriginalURL, http.StatusFound)

	// Событие клика — строго ПОСЛЕ ответа клиенту. Publish не блокирует
	// (буфер franz-go); ошибка сборки не влияет на уже отданный редирект.
	if h.builder != nil && h.pub != nil {
		ev, err := h.builder.Build(res.LinkID, code, res.OriginalURL, r)
		if err != nil {
			h.log.ErrorContext(r.Context(), "сборка ClickEvent",
				"error", err, "short_code", code, "request_id", RequestIDFrom(r.Context()))
			return
		}
		h.pub.Publish(ev)
	}
}

// Pinger — зависимость, умеющая отвечать на ping (Postgres, Redis).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Healthz — GET /healthz: 200 если все зависимости отвечают, иначе 503.
// На каждый ping — короткий таймаут, чтобы health-проверка не зависала.
func (h *Handlers) Healthz(deps map[string]Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status := make(map[string]string, len(deps))
		healthy := true
		for name, dep := range deps {
			if err := dep.Ping(ctx); err != nil {
				status[name] = err.Error()
				healthy = false
			} else {
				status[name] = "ok"
			}
		}

		code := http.StatusOK
		if !healthy {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, status)
	}
}

// ── Вспомогательные ───────────────────────────────────────────────────────────

// writeDomainError транслирует sentinel-ошибки домена в HTTP-статусы.
func (h *Handlers) writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, link.ErrInvalidURL):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, link.ErrNotFound):
		writeError(w, http.StatusNotFound, "ссылка не найдена")
	case errors.Is(err, link.ErrExpired):
		writeError(w, http.StatusGone, "срок действия ссылки истёк")
	default:
		// Внутренние детали не отдаём клиенту — только в лог.
		h.log.ErrorContext(r.Context(), "внутренняя ошибка",
			"error", err, "request_id", RequestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
