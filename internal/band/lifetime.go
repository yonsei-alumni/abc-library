package band

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// sessionCookies는 로그인 상태를 정하는 쿠키 이름이다.
var sessionCookies = []string{"band_session", "rt", "SESSION"}

// SessionLifetime은 세션 쿠키 하나의 만료 정보다. 쿠키 값은 담지 않는다.
type SessionLifetime struct {
	Name string
	// CookieExpires는 쿠키 자체의 만료 시각이다. 브라우저를 닫으면 사라지는 세션 쿠키면 0이다.
	CookieExpires time.Time
	// TokenExp는 값이 JWT일 때 payload의 exp다. JWT가 아니거나 exp가 없으면 0이다.
	TokenExp time.Time
}

// SessionLifetimes는 statePath의 세션 쿠키가 언제 끝나는지 읽는다. '로그인 유지'가 먹혔는지 확인하는 데 쓴다.
func SessionLifetimes(statePath string) ([]SessionLifetime, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	var state struct {
		Cookies []struct {
			Name    string  `json:"name"`
			Value   string  `json:"value"`
			Expires float64 `json:"expires"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	var out []SessionLifetime
	for _, name := range sessionCookies {
		for _, c := range state.Cookies {
			if c.Name != name {
				continue
			}
			l := SessionLifetime{Name: name, TokenExp: jwtExp(c.Value)}
			if c.Expires > 0 {
				l.CookieExpires = time.Unix(int64(c.Expires), 0)
			}
			out = append(out, l)
		}
	}
	return out, nil
}

func jwtExp(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// Describe는 한 줄 요약이다. now는 남은 시간을 계산하는 기준이다.
func (l SessionLifetime) Describe(now time.Time) string {
	cookie := "세션 쿠키(브라우저를 닫으면 끝)"
	if !l.CookieExpires.IsZero() {
		cookie = "쿠키 만료 " + l.CookieExpires.Format("2006-01-02 15:04")
	}
	token := "토큰 만료 정보 없음"
	if !l.TokenExp.IsZero() {
		token = fmt.Sprintf("토큰 만료 %s(%s 뒤)", l.TokenExp.Format("2006-01-02 15:04"), l.TokenExp.Sub(now).Round(time.Minute))
	}
	return fmt.Sprintf("%s: %s, %s", l.Name, cookie, token)
}
