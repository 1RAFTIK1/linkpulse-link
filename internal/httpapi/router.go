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
// auth — middleware авторизации: BearerAuth (прод) или StubAuth (dev без Auth).
// Редирект auth не требует — он публичный по определению.
func NewRouter(h *Handlers, log *slog.Logger, deps map[string]Pinger, auth func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()

	// Защищённые endpoint'ы.
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
