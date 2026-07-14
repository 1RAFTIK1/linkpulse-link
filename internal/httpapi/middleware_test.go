package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeValidator — управляемая замена gRPC-клиента к Auth.
type fakeValidator struct {
	userID string
	valid  bool
	err    error
	gotTok string
}

func (f *fakeValidator) Validate(_ context.Context, token string) (string, bool, error) {
	f.gotTok = token
	return f.userID, f.valid, f.err
}

func TestBearerAuth(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	echoUser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(UserIDFrom(r.Context())))
	})

	tests := []struct {
		name       string
		authHeader string
		v          *fakeValidator
		wantStatus int
		wantBody   string
	}{
		{
			name:       "валидный токен",
			authHeader: "Bearer good-token",
			v:          &fakeValidator{userID: "42", valid: true},
			wantStatus: http.StatusOK,
			wantBody:   "42",
		},
		{
			name:       "нет заголовка",
			authHeader: "",
			v:          &fakeValidator{},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "не Bearer",
			authHeader: "Basic dXNlcjpwYXNz",
			v:          &fakeValidator{},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "невалидный токен",
			authHeader: "Bearer expired",
			v:          &fakeValidator{valid: false},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "auth недоступен",
			authHeader: "Bearer any",
			v:          &fakeValidator{err: errors.New("connection refused")},
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/links", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			BearerAuth(tt.v, log)(echoUser).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("статус %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body %q, want %q", rec.Body.String(), tt.wantBody)
			}
			if tt.wantStatus == http.StatusOK && tt.v.gotTok != "good-token" {
				t.Errorf("валидатору передан %q", tt.v.gotTok)
			}
		})
	}
}
