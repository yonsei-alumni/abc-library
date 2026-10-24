// Package auth는 Google 계정 로그인이다(OpenID Connect). 서명한 쿠키로 세션을 유지하고,
// 사용자는 Google 계정 고유 번호(sub)를 HMAC-SHA256한 값으로 구별한다.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

// Session은 쿠키에 담는 세션이다. 로그인하면 User가 있고, 관리자로 로그인하면 Email도 있다.
// 로그인하는 중에는 State와 Next가 있다.
type Session struct {
	// User는 users 테이블의 번호다.
	User  int64  `json:"u,omitempty"`
	Email string `json:"e,omitempty"`
	// State는 Google 로그인 요청과 응답을 잇는 임의의 값이다. 다른 사이트가 꾸민 응답을 막는다.
	State string `json:"s,omitempty"`
	// Next는 로그인한 뒤 돌아갈 경로다.
	Next string `json:"n,omitempty"`
	// AdminLogin은 관리자 로그인 중이라는 뜻이다. 관리자 로그인만 메일 주소를 받는다.
	AdminLogin bool  `json:"a,omitempty"`
	Expires    int64 `json:"x"`
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

// AuthCodeURL은 Google 로그인 화면의 주소다. email이 참이면 메일 주소도 달라고 한다(관리자 로그인).
// 거짓이면 범위가 openid뿐이라서 Google이 메일 주소를 보내지 않는다.
func (g *Google) AuthCodeURL(state string, email bool) string {
	authURL := g.AuthURL
	if authURL == "" {
		authURL = googleAuthURL
	}
	scope := "openid"
	if email {
		scope = "openid email"
	}
	q := url.Values{
		"client_id":     {g.ClientID},
		"redirect_uri":  {g.RedirectURL},
		"response_type": {"code"},
		"scope":         {scope},
		"state":         {state},
		"prompt":        {"select_account"},
	}
	return authURL + "?" + q.Encode()
}

// Account는 Google이 확인해 준 계정이다. Subject는 계정 고유 번호(sub)이고,
// Email은 메일 주소를 달라고 했고 Google이 확인한 주소일 때만 소문자로 들어 있다.
type Account struct {
	Subject string
	Email   string
}

// Exchange는 로그인 화면이 돌려준 code를 ID 토큰으로 바꾸고 계정을 돌려준다.
// ID 토큰은 TLS로 Google의 토큰 엔드포인트에서 직접 받으므로 서명 대신 발급자, 대상, 기한을 확인한다
// (OpenID Connect Core 3.1.3.7).
func (g *Google) Exchange(ctx context.Context, code string) (Account, error) {
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
		return Account{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Account{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Account{}, fmt.Errorf("google 토큰 요청 실패: %s", resp.Status)
	}
	var token struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.IDToken == "" {
		return Account{}, errors.New("google 토큰 응답에 ID 토큰이 없습니다")
	}
	return g.verify(token.IDToken, time.Now())
}

var googleIssuers = []string{"https://accounts.google.com", "accounts.google.com"}

// verify는 ID 토큰의 내용을 확인하고 계정을 돌려준다.
func (g *Google) verify(idToken string, now time.Time) (Account, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return Account{}, errors.New("ID 토큰 형식이 잘못되었습니다")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Account{}, errors.New("ID 토큰 형식이 잘못되었습니다")
	}
	var claims struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Exp           int64  `json:"exp"`
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return Account{}, errors.New("ID 토큰 형식이 잘못되었습니다")
	}
	switch {
	case !slices.Contains(googleIssuers, claims.Iss):
		return Account{}, errors.New("ID 토큰의 발급자가 Google이 아닙니다")
	case claims.Aud != g.ClientID:
		return Account{}, errors.New("ID 토큰이 이 사이트에 발급된 것이 아닙니다")
	case now.Unix() >= claims.Exp:
		return Account{}, errors.New("ID 토큰의 기한이 지났습니다")
	case claims.Sub == "":
		return Account{}, errors.New("ID 토큰에 계정 고유 번호가 없습니다")
	}
	a := Account{Subject: claims.Sub}
	if claims.EmailVerified {
		a.Email = strings.ToLower(claims.Email)
	}
	return a, nil
}

// Subjects는 Google 계정 고유 번호(sub)를 서버의 비밀 키로 HMAC-SHA256해서 사용자를 구별하는 값을 만든다.
// DB에는 이 값만 저장하므로 DB만으로는 어느 Google 계정인지 알 수 없다.
// 키를 바꾸면 모든 사용자가 새 사용자가 되어 북마크를 잃으므로 바꾸지 않는다.
type Subjects struct {
	key []byte
}

// NewSubjects는 비밀 값으로 Subjects를 만든다.
func NewSubjects(secret string) (*Subjects, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("사용자 구별 비밀 값은 %d바이트 이상이어야 합니다", MinSecretLen)
	}
	return &Subjects{key: []byte(secret)}, nil
}

// Hash는 sub의 HMAC-SHA256 값을 16진수로 돌려준다.
func (s *Subjects) Hash(sub string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte("google:" + sub))
	return hex.EncodeToString(mac.Sum(nil))
}
