package clicks

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/oschwald/geoip2-golang/v2"
)

// GeoIP — CountryResolver поверх локальной базы MaxMind GeoLite2-Country.
// Оффлайн-lookup по mmap-файлу: микросекунды, без сетевых вызовов на пути
// редиректа. Файл базы скачивается с MaxMind (бесплатная лицензия GeoLite2)
// и подключается через GEOIP_DB_PATH; без него сервис работает с NoopResolver.
type GeoIP struct {
	reader *geoip2.Reader
}

func NewGeoIP(dbPath string) (*GeoIP, error) {
	reader, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("открытие GeoLite2 базы: %w", err)
	}
	return &GeoIP{reader: reader}, nil
}

func (g *GeoIP) Country(ip net.IP) string {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return ""
	}
	rec, err := g.reader.Country(addr)
	if err != nil || rec == nil {
		return ""
	}
	return rec.Country.ISOCode
}

func (g *GeoIP) Close() error { return g.reader.Close() }

var _ CountryResolver = (*GeoIP)(nil)
