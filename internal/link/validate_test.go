package link

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeAndValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"обычный https", "https://example.com/path?q=1", false},
		{"обычный http", "http://example.com", false},
		{"с пробелами по краям", "  https://example.com  ", false},
		{"пустой", "", true},
		{"только пробелы", "   ", true},
		{"без схемы", "example.com", true},
		{"ftp-схема", "ftp://example.com", true},
		{"javascript", "javascript:alert(1)", true},
		{"схема без хоста", "http://", true},
		{"localhost", "http://localhost:8080/admin", true},
		{"поддомен localhost", "http://api.localhost/", true},
		{"loopback ip", "http://127.0.0.1/", true},
		{"приватный ip 10", "http://10.0.0.5/", true},
		{"приватный ip 192.168", "https://192.168.1.1/", true},
		{"link-local", "http://169.254.169.254/latest/meta-data", true},
		{"unspecified", "http://0.0.0.0/", true},
		{"ipv6 loopback", "http://[::1]/", true},
		{"публичный ip", "http://8.8.8.8/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeAndValidateURL(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку, получили nil (результат %q)", got)
				}
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("ошибка должна оборачивать ErrInvalidURL, получили %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if strings.TrimSpace(got) == "" {
				t.Fatal("нормализованный URL пуст")
			}
		})
	}
}
