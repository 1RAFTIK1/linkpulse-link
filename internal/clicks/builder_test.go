package clicks

import (
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

type stubIDs struct{ id int64 }

func (s *stubIDs) Next() (int64, error) { return s.id, nil }

type stubGeo struct{ country string }

func (s stubGeo) Country(net.IP) string { return s.country }

func TestBuild_FillsAllFields(t *testing.T) {
	b := NewBuilder(&stubIDs{id: 777}, "salt", stubGeo{country: "DE"})
	fixed := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return fixed }

	r := httptest.NewRequest("GET", "/abc", nil)
	r.RemoteAddr = "203.0.113.7:51000"
	r.Header.Set("Referer", "https://t.me/somechannel")
	r.Header.Set("User-Agent", "test-agent/1.0")

	ev, err := b.Build(42, "abc", "https://example.com", r)
	if err != nil {
		t.Fatal(err)
	}

	if ev.GetEventId() != 777 || ev.GetLinkId() != 42 {
		t.Errorf("ids: event=%d link=%d", ev.GetEventId(), ev.GetLinkId())
	}
	if ev.GetShortCode() != "abc" || ev.GetOriginalUrl() != "https://example.com" {
		t.Errorf("link data: %q %q", ev.GetShortCode(), ev.GetOriginalUrl())
	}
	if !ev.GetClickedAt().AsTime().Equal(fixed) {
		t.Errorf("clicked_at=%v", ev.GetClickedAt().AsTime())
	}
	if ev.GetReferrer() != "https://t.me/somechannel" || ev.GetUserAgent() != "test-agent/1.0" {
		t.Errorf("http data: %q %q", ev.GetReferrer(), ev.GetUserAgent())
	}
	if ev.GetCountry() != "DE" {
		t.Errorf("country=%q", ev.GetCountry())
	}
	if len(ev.GetIpHash()) != 64 { // hex(sha256) = 64 символа
		t.Errorf("ip_hash=%q", ev.GetIpHash())
	}
}

func TestHashIP_SaltChangesHash(t *testing.T) {
	ip := net.ParseIP("203.0.113.7")
	h1, h2 := hashIP("salt-a", ip), hashIP("salt-b", ip)
	if h1 == h2 {
		t.Error("хеши с разными солями не должны совпадать")
	}
	if hashIP("salt-a", ip) != h1 {
		t.Error("хеш не детерминирован")
	}
	if hashIP("salt", nil) != "" {
		t.Error("nil IP должен давать пустой хеш")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"только RemoteAddr", "203.0.113.7:5100", "", "203.0.113.7"},
		{"XFF один адрес", "10.0.0.1:80", "198.51.100.4", "198.51.100.4"},
		{"XFF цепочка — берём первый", "10.0.0.1:80", "198.51.100.4, 10.0.0.2, 10.0.0.3", "198.51.100.4"},
		{"XFF мусор — фолбэк на RemoteAddr", "203.0.113.7:5100", "not-an-ip", "203.0.113.7"},
		{"ipv6 RemoteAddr", "[2001:db8::1]:443", "", "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/x", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			got := clientIP(r)
			if got == nil || got.String() != tt.want {
				t.Errorf("clientIP=%v, want %s", got, tt.want)
			}
		})
	}
}
