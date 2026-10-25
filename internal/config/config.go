// Package config는 환경 변수로 받은 설정 속성을 읽는다.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
)

// DatabaseURL은 DB_URL에 DB_PWD의 비밀번호를 넣은 연결 문자열을 만든다.
// DB_URL에는 비밀번호를 빼고 사용자 이름까지만 적는다.
func DatabaseURL() (string, error) {
	raw := os.Getenv("DB_URL")
	if raw == "" {
		return "", errors.New("DB_URL 환경 변수가 없습니다")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("DB_URL 형식이 잘못되었습니다: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return "", errors.New("DB_URL에 사용자 이름이 없습니다")
	}
	if _, ok := u.User.Password(); ok {
		return "", errors.New("DB_URL에는 비밀번호를 넣지 말고 DB_PWD로 지정해야 합니다")
	}
	pwd := os.Getenv("DB_PWD")
	if pwd == "" {
		return "", errors.New("DB_PWD 환경 변수가 없습니다")
	}
	u.User = url.UserPassword(u.User.Username(), pwd)
	return u.String(), nil
}

// BandNo는 수집하고 원문 링크를 만들 ABC 밴드의 번호다(BAND_NO).
func BandNo() string {
	return Get("BAND_NO", "102765569")
}

// Get은 환경 변수의 값을 돌려주고, 값이 없으면 기본값을 돌려준다.
func Get(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
