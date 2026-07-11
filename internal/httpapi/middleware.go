package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUserID
)

// requestIDHeader — заголовок, в котором request_id приходит от клиента/прокси
// и возвращается в ответе. Дальше он поедет в Kafka-заголовки и gRPC-метаданные —
// сквозной способ грепнуть один запрос по логам всех сервисов (см. спека §10).
const requestIDHeader = "X-Request-Id"

// RequestID берёт request_id из входящего заголовка или генерирует новый,
// кладёт его в context и в заголовок ответа.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RequestIDFrom достаёт request_id из context ("" если нет).
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID).(string)
	return id
}

// statusRecorder запоминает код ответа для лога.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Logging пишет структурированную запись на каждый запрос: метод, путь,
// статус, длительность, request_id.
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			log.InfoContext(r.Context(), "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFrom(r.Context()),
			)
		})
	}
}

// Recover перехватывает панику в handler'е: 500 клиенту и запись в лог
// вместо падения всего процесса.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					log.ErrorContext(r.Context(), "паника в handler",
						"panic", p,
						"path", r.URL.Path,
						"request_id", RequestIDFrom(r.Context()),
					)
					writeError(w, http.StatusInternalServerError, "internal error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// StubAuth — заглушка авторизации фазы 2 (спека §13, фаза 2): кладёт в context
// фиксированный user_id, чтобы не блокироваться на Auth service. В фазе 5
// заменяется на настоящую проверку JWT через gRPC ValidateToken.
func StubAuth(userID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxKeyUserID, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFrom достаёт user_id авторизованного пользователя из context.
func UserIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyUserID).(string)
	return id
}
