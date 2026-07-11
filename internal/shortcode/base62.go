// Package shortcode кодирует числовой Snowflake ID в компактный base62-код,
// который и становится коротким кодом ссылки (часть short_url).
//
// base62 = [0-9A-Za-z]: только URL-безопасные символы, без спецкодирования,
// плотнее base16/base36. 63-битный ID укладывается максимум в 11 символов.
package shortcode

import (
	"fmt"
	"strings"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const base = int64(len(alphabet)) // 62

// Encode переводит неотрицательное число в base62-строку.
func Encode(id int64) string {
	if id == 0 {
		return alphabet[:1]
	}
	// Собираем цифры от младшей к старшей, потом разворачиваем.
	buf := make([]byte, 0, 11)
	for id > 0 {
		buf = append(buf, alphabet[id%base])
		id /= base
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// Decode переводит base62-строку обратно в число. Ошибка — если встречен символ
// не из алфавита. Используется в тестах и для быстрой валидации формата кода.
func Decode(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("shortcode: пустая строка")
	}
	var n int64
	for _, r := range s {
		idx := strings.IndexRune(alphabet, r)
		if idx < 0 {
			return 0, fmt.Errorf("shortcode: недопустимый символ %q", r)
		}
		n = n*base + int64(idx)
	}
	return n, nil
}
