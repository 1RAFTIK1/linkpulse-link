package httpapi

import (
	"log/slog"
	"net/http"
)

// NewRouter собирает роутер сервиса на stdlib http.ServeMux.
//
// С Go 1.22 mux понимает метод и path-параметры в паттерне ("POST /api/v1/links",
// "GET /{code}") — сторонний роутер не нужен (меньше зависимостей, идиоматичнее).
//
// stubUserID — заглушка авторизации фазы 2; в фазе 5 вместо StubAuth встанет
// middleware с настоящей проверкой JWT.
func NewRouter(h *Handlers, log *slog.Logger, deps map[string]Pinger, stubUserID string) http.Handler {
	mux := http.NewServeMux()

	auth := StubAuth(stubUserID)

	// Защищённые endpoint'ы (в фазе 5 — реальный Bearer JWT).
	mux.Handle("POST /api/v1/links", auth(http.HandlerFunc(h.CreateLink)))
	mux.Handle("GET /api/v1/links", auth(http.HandlerFunc(h.ListLinks)))

	// Служебные.
	mux.HandleFunc("GET /healthz", h.Healthz(deps))

	// Публичный редирект. Регистрируем последним по смыслу: паттерн "GET /{code}"
	// — самый общий; mux сам выбирает наиболее специфичный маршрут, так что
	// /healthz и /api/... не перехватываются.
	mux.HandleFunc("GET /{code}", h.Redirect)

	// Сквозные middleware: recover снаружи (ловит панику в том числе из logging),
	// затем request_id (нужен логированию), затем логирование.
	var handler http.Handler = mux
	handler = Logging(log)(handler)
	handler = RequestID(handler)
	handler = Recover(log)(handler)
	return handler
}
