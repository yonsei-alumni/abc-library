package web

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/yonsei-alumni/abc-library/internal/auth"
)

// maxUserForm은 사용자 화면의 폼 크기 상한이다. 카테고리 본문(book.MaxCategoryBody자)의 한글은
// URL 인코딩하면 한 글자가 9바이트가 되므로 관리 화면보다 넉넉하게 둔다.
const maxUserForm = 256 << 10

type userKey struct{}

// userID는 로그인한 사용자의 번호다. 로그인하지 않았으면 0이다.
func userID(r *http.Request) int64 {
	id, _ := r.Context().Value(userKey{}).(int64)
	return id
}

// withAccount는 세션 쿠키의 계정을 요청에 싣는다. 관리자이면 메일 주소를, 사용자이면 번호를 싣는다.
// 관리자 목록과 사용자는 요청마다 DB에서 확인하므로, 관리자에서 빼거나 계정을 지우면 바로 권한이 없어진다.
func (s *Server) withAccount(next http.Handler) http.Handler {
	if s.sessions == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.sessions.Read(r)
		if !ok || (sess.Email == "" && sess.User == 0) {
			next.ServeHTTP(w, r)
			return
		}
		// 로그인한 사람에게 보인 화면은 Firebase Hosting이나 브라우저가 캐시하지 않게 한다.
		w.Header().Set("Cache-Control", "private, no-store")
		ctx := r.Context()
		if sess.Email != "" {
			admin, err := s.store.IsAdmin(ctx, sess.Email)
			if err != nil {
				s.log.Error("관리자인지 확인하지 못했다", "오류", err)
			}
			if admin {
				ctx = context.WithValue(ctx, adminKey{}, sess.Email)
			}
		}
		if sess.User != 0 && s.subjects != nil {
			exists, err := s.store.UserExists(ctx, sess.User)
			if err != nil {
				s.log.Error("사용자를 확인하지 못했다", "오류", err)
			}
			if exists {
				ctx = context.WithValue(ctx, userKey{}, sess.User)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireUser는 로그인한 사용자만 h를 쓰게 한다. 로그인하지 않았으면 GET 요청은 로그인 화면으로 보낸다.
func (s *Server) requireUser(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		switch {
		case s.subjects == nil:
			s.loginUnavailable(w, r)
		case userID(r) != 0:
			r.Body = http.MaxBytesReader(w, r.Body, maxUserForm)
			h(w, r)
		case r.Method == http.MethodGet:
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		default:
			s.status(w, r, http.StatusForbidden, "로그인해 주세요", "로그인한 뒤에 할 수 있습니다.")
		}
	})
}

func (s *Server) accountRoutes(mux *http.ServeMux) {
	// 로그인을 설정하지 않았으면 로그인 화면과 Google의 응답을 받는 주소는 없는 것으로 다룬다.
	login := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if s.login == nil {
				s.loginUnavailable(w, r)
				return
			}
			h(w, r)
		})
	}
	login("GET /login", s.loginPage)
	login("POST /login", s.loginStart)
	login("GET /auth/callback", s.loginCallback)
	mux.HandleFunc("POST /logout", s.logout)
}

func (s *Server) loginUnavailable(w http.ResponseWriter, r *http.Request) {
	s.status(w, r, http.StatusNotFound, "찾을 수 없습니다", "로그인이 설정되지 않았습니다.")
}

// safeNext는 로그인한 뒤 돌아갈 경로다. 다른 사이트로 가는 주소는 받지 않는다.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

// adminLogin은 관리자 로그인 요청인지 알려 준다. 사용자 로그인을 쓰지 않으면 로그인은 모두 관리자 로그인이다.
func (s *Server) adminLogin(v string) bool {
	return v == "1" || s.subjects == nil
}

// loginPage는 Google로 넘어가기 전에 무엇을 받고 저장하는지 알리는 화면이다.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	next := safeNext(q.Get("next"))
	admin := s.adminLogin(q.Get("admin"))
	if (admin && adminEmail(r) != "") || (!admin && userID(r) != 0) {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	title := "로그인"
	if admin {
		title = "관리자 로그인"
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, "login", "layout", map[string]any{"Title": title, "AdminLogin": admin, "Next": next})
}

func (s *Server) loginStart(w http.ResponseWriter, r *http.Request) {
	admin := s.adminLogin(r.PostFormValue("admin"))
	state := auth.NewState()
	s.sessions.Set(w, auth.Session{State: state, Next: safeNext(r.PostFormValue("next")), AdminLogin: admin}, loginTTL)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.login.AuthCodeURL(state, admin), http.StatusSeeOther)
}

func (s *Server) loginCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	if q.Get("error") != "" {
		s.status(w, r, http.StatusBadRequest, "로그인하지 못했습니다", "Google 로그인을 취소했습니다.")
		return
	}
	login, ok := s.sessions.Read(r)
	if !ok || login.State == "" || subtle.ConstantTimeCompare([]byte(login.State), []byte(q.Get("state"))) != 1 {
		s.status(w, r, http.StatusBadRequest, "로그인하지 못했습니다", "로그인 요청이 만료되었습니다. 다시 시도해 주세요.")
		return
	}
	account, err := s.login.Exchange(r.Context(), q.Get("code"))
	if err != nil {
		s.log.Warn("Google 로그인을 확인하지 못했다", "오류", err)
		s.status(w, r, http.StatusBadRequest, "로그인하지 못했습니다", "Google 로그인을 확인하지 못했습니다. 다시 시도해 주세요.")
		return
	}
	var sess auth.Session
	if login.AdminLogin {
		if account.Email == "" {
			s.status(w, r, http.StatusBadRequest, "로그인하지 못했습니다", "확인된 메일 주소가 없는 계정입니다.")
			return
		}
		admin, err := s.store.IsAdmin(r.Context(), account.Email)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !admin {
			s.sessions.Clear(w)
			s.status(w, r, http.StatusForbidden, "권한이 없습니다", "관리자로 등록되지 않은 계정입니다.")
			return
		}
		sess.Email = account.Email
	}
	// 관리자도 북마크를 쓸 수 있게 사용자로 함께 로그인한다. 메일 주소는 사용자에 저장하지 않는다.
	if s.subjects != nil {
		id, err := s.store.UserID(r.Context(), s.subjects.Hash(account.Subject))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		sess.User = id
	}
	s.sessions.Set(w, sess, sessionTTL)
	http.Redirect(w, r, safeNext(login.Next), http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.sessions != nil {
		s.sessions.Clear(w)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
