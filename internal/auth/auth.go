// Package auth는 관리자 로그인이다. Google 계정으로 로그인하고(OpenID Connect), 서명한 쿠키로 세션을 유지한다.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// CookieName은 세션 쿠키의 이름이다. Firebase Hosting은 Cloud Run으로 요청을 넘길 때
// __session 쿠키만 남기고 다른 쿠키는 지우므로 이 이름만 쓸 수 있다.
const CookieName = "__session"

// Session은 쿠키에 담는 세션이다. 로그인하면 Email이 있고, 로그인하는 중에는 State와 Next가 있다.
type Session struct {
	Email string `json:"e,omitempty"`
	// State는 Google 로그인 요청과 응답을 잇는 임의의 값이다. 다른 사이트가 꾸민 응답을 막는다.
	State string `json:"s,omitempty"`
	// Next는 로그인한 뒤 돌아갈 경로다.
	Next    string `json:"n,omitempty"`
	Expires int64  `json:"x"`
}

// Sessions는 세션 쿠키를 HMAC-SHA256으로 서명하고 검증한다.
type Sessions struct {
	key    []byte
	secure bool
	now    func() time.Time
}

// MinSecretLen은 세션 서명 비밀 값의 최소 길이다.
const MinSecretLen = 32

// NewSessions는 비밀 값으로 Sessions를 만든다. secure가 참이면 HTTPS에서만 쿠키를 보낸다.
func NewSessions(secret string, secure bool) (*Sessions, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("세션 비밀 값은 %d바이트 이상이어야 합니다", MinSecretLen)
	}
	return &Sessions{key: []byte(secret), secure: secure, now: time.Now}, nil
}

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Sessions) encode(sess Session) string {
	data, _ := json.Marshal(sess)
	payload := base64.RawURLEncoding.EncodeToString(data)
	return payload + "." + s.sign(payload)
}

func (s *Sessions) decode(value string) (Session, bool) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return Session{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Session{}, false
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil || s.now().Unix() >= sess.Expires {
		return Session{}, false
	}
	return sess, true
}

// Set은 ttl 동안 쓸 수 있는 세션 쿠키를 응답에 싣는다.
func (s *Sessions) Set(w http.ResponseWriter, sess Session, ttl time.Duration) {
	expires := s.now().Add(ttl)
	sess.Expires = expires.Unix()
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: s.encode(sess), Path: "/", Expires: expires, MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
}

// Read는 요청의 세션 쿠키를 읽는다. 쿠키가 없거나, 서명이 틀리거나, 기한이 지났으면 ok가 거짓이다.
func (s *Sessions) Read(r *http.Request) (Session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, false
	}
	return s.decode(c.Value)
}

// Clear는 세션 쿠키를 지운다.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
}

// NewState는 로그인 요청마다 새로 만드는 임의의 값이다.
func NewState() string {
	return rand.Text()
}

// Google은 Google 계정 로그인(OpenID Connect의 authorization code 흐름)이다.
type Google struct {
	ClientID     string
	ClientSecret string
	// RedirectURL은 Google 클라이언트에 등록한 승인된 리디렉션 URI다.
	RedirectURL string
	// AuthURL과 TokenURL이 비어 있으면 Google의 주소를 쓴다. 테스트에서 바꾼다.
	AuthURL  string
	TokenURL string
	HTTP     *http.Client
}

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
)

// AuthCodeURL은 Google 로그인 화면의 주소다.
func (g *Google) AuthCodeURL(state string) string {
	authURL := g.AuthURL
	if authURL == "" {
		authURL = googleAuthURL
	}
	q := url.Values{
		"client_id":     {g.ClientID},
		"redirect_uri":  {g.RedirectURL},
		"response_type": {"code"},
		"scope":         {"openid email"},
		"state":         {state},
		"prompt":        {"select_account"},
	}
	return authURL + "?" + q.Encode()
}

// Exchange는 로그인 화면이 돌려준 code를 ID 토큰으로 바꾸고, 확인된 메일 주소를 소문자로 돌려준다.
// ID 토큰은 TLS로 Google의 토큰 엔드포인트에서 직접 받으므로 서명 대신 발급자, 대상, 기한을 확인한다
// (OpenID Connect Core 3.1.3.7).
func (g *Google) Exchange(ctx context.Context, code string) (string, error) {
	tokenURL := g.TokenURL
	if tokenURL == "" {
		tokenURL = googleTokenURL
	}
	form := url.Values{
		"code":          {code},
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"redirect_uri":  {g.RedirectURL},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google 토큰 요청 실패: %s", resp.Status)
	}
	var token struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.IDToken == "" {
		return "", errors.New("google 토큰 응답에 ID 토큰이 없습니다")
	}
	return g.verify(token.IDToken, time.Now())
}

var googleIssuers = []string{"https://accounts.google.com", "accounts.google.com"}

// verify는 ID 토큰의 내용을 확인하고 메일 주소를 돌려준다.
func (g *Google) verify(idToken string, now time.Time) (string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", errors.New("ID 토큰 형식이 잘못되었습니다")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("ID 토큰 형식이 잘못되었습니다")
	}
	var claims struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Exp           int64  `json:"exp"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return "", errors.New("ID 토큰 형식이 잘못되었습니다")
	}
	switch {
	case !slices.Contains(googleIssuers, claims.Iss):
		return "", errors.New("ID 토큰의 발급자가 Google이 아닙니다")
	case claims.Aud != g.ClientID:
		return "", errors.New("ID 토큰이 이 사이트에 발급된 것이 아닙니다")
	case now.Unix() >= claims.Exp:
		return "", errors.New("ID 토큰의 기한이 지났습니다")
	case claims.Email == "" || !claims.EmailVerified:
		return "", errors.New("확인된 메일 주소가 없는 계정입니다")
	}
	return strings.ToLower(claims.Email), nil
}
