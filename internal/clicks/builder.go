package clicks

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/1RAFTIK1/linkpulse-contracts/gen/go/events/v1"
)

// CountryResolver определяет страну по IP. Реализации: GeoIP (MaxMind GeoLite2)
// и NoopResolver (когда база не подключена).
type CountryResolver interface {
	Country(ip net.IP) string // ISO 3166-1 alpha-2, "" если неизвестно
}

// NoopResolver — заглушка: страна всегда неизвестна.
type NoopResolver struct{}

func (NoopResolver) Country(net.IP) string { return "" }

// IDGenerator — источник event_id (Snowflake-генератор Link service).
type IDGenerator interface {
	Next() (int64, error)
}

// Builder собирает ClickEvent из данных ссылки и HTTP-запроса.
type Builder struct {
	ids  IDGenerator
	salt string // соль для хеша IP — из конфига, в проде секрет
	geo  CountryResolver
	now  func() time.Time
}

func NewBuilder(ids IDGenerator, salt string, geo CountryResolver) *Builder {
	return &Builder{ids: ids, salt: salt, geo: geo, now: time.Now}
}

// Build формирует событие клика. Страна резолвится ДО хеширования IP —
// после sha256 геолокация уже невозможна (в этом и смысл хеша).
func (b *Builder) Build(linkID int64, shortCode, originalURL string, r *http.Request) (*eventsv1.ClickEvent, error) {
	eventID, err := b.ids.Next()
	if err != nil {
		return nil, err
	}

	ip := clientIP(r)

	return &eventsv1.ClickEvent{
		EventId:     eventID,
		LinkId:      linkID,
		ShortCode:   shortCode,
		OriginalUrl: originalURL,
		ClickedAt:   timestamppb.New(b.now().UTC()),
		Referrer:    r.Referer(),
		IpHash:      hashIP(b.salt, ip),
		UserAgent:   r.UserAgent(),
		Country:     b.geo.Country(ip),
	}, nil
}

// clientIP извлекает IP клиента. Берём первый адрес из X-Forwarded-For
// (его выставляет reverse-proxy; в проде заголовку доверяют только за своим
// прокси), иначе — RemoteAddr соединения.
func clientIP(r *http.Request) net.IP {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr // RemoteAddr без порта — маловероятно, но не падаем
	}
	return net.ParseIP(host)
}

// hashIP — sha256(salt + ip). Соль обязательна: пространство IPv4 всего ~4 млрд
// значений, несолёный хеш обращается брутфорсом за минуты.
func hashIP(salt string, ip net.IP) string {
	if ip == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(salt + ip.String()))
	return hex.EncodeToString(sum[:])
}
