package link

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// maxURLLength — верхняя граница длины исходного URL. Защищает от заведомо
// мусорных значений и раздувания БД.
const maxURLLength = 2048

// NormalizeAndValidateURL проверяет пользовательский URL перед сохранением и
// возвращает нормализованную форму.
//
// Требуем явную схему http/https и отклоняем ссылки на внутренние адреса
// (localhost, приватные/loopback IP). Хотя редирект выполняет браузер клиента,
// а не сервер (то есть классический SSRF тут не применим), не даём использовать
// шортенер для маскировки ссылок на внутренние ресурсы.
//
// DNS намеренно не резолвим: это добавило бы сетевой вызов на путь создания и
// зависимость от момента резолва. Ограничиваемся проверкой литеральных адресов.
func NormalizeAndValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: пустой URL", ErrInvalidURL)
	}
	if len(raw) > maxURLLength {
		return "", fmt.Errorf("%w: длина больше %d символов", ErrInvalidURL, maxURLLength)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if scheme := strings.ToLower(u.Scheme); scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: схема %q, разрешены только http/https", ErrInvalidURL, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("%w: отсутствует хост", ErrInvalidURL)
	}
	if isBlockedHost(host) {
		return "", fmt.Errorf("%w: хост %q запрещён (внутренний адрес)", ErrInvalidURL, host)
	}

	return u.String(), nil
}

// isBlockedHost отсекает localhost и литеральные внутренние IP.
func isBlockedHost(host string) bool {
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || // 127.0.0.0/8, ::1
			ip.IsPrivate() || // 10/8, 172.16/12, 192.168/16, fc00::/7
			ip.IsUnspecified() || // 0.0.0.0, ::
			ip.IsLinkLocalUnicast() || // 169.254/16, fe80::/10
			ip.IsLinkLocalMulticast()
	}
	return false
}
