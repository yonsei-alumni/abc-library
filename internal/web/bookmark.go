package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

func (s *Server) bookmarkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /shared", s.sharedList)
	mux.HandleFunc("GET /shared/{code}", s.shared)

	user := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireUser(h)) }
	user("GET /me", func(w http.ResponseWriter, r *http.Request) { s.renderMe(w, r, http.StatusOK, "") })
	user("POST /me/categories", s.addCategory)
	user("POST /me/categories/move", s.moveCategory)
	user("GET /me/categories/{id}", s.myCategory)
	user("POST /me/categories/{id}", s.saveCategory)
	user("POST /me/categories/{id}/delete", s.deleteCategory)
	user("POST /me/bookmarks/order", s.reorderBookmarks)
	user("POST /me/bookmarks/{id}", s.saveBookmark)
	user("POST /me/bookmarks/{id}/delete", s.deleteBookmark)
	user("POST /me/delete", s.deleteAccount)
}

// categoryOption은 책 화면에서 고를 수 있는 내 카테고리다.
type categoryOption struct {
	ID      int64
	Title   string
	Checked bool
}

// bookmarkPanel은 책 화면에 보이는 내 북마크 상태다. In은 이 책을 넣은 카테고리다.
type bookmarkPanel struct {
	Marked     bool
	Categories []categoryOption
	In         []categoryOption
}

func (s *Server) bookmarkPanel(ctx context.Context, uid, bookID int64) (bookmarkPanel, error) {
	marked, ids, err := s.store.Bookmark(ctx, uid, bookID)
	if err != nil {
		return bookmarkPanel{}, err
	}
	cs, err := s.store.Categories(ctx, uid)
	if err != nil {
		return bookmarkPanel{}, err
	}
	checked := map[int64]bool{}
	for _, id := range ids {
		checked[id] = true
	}
	p := bookmarkPanel{Marked: marked}
	for _, c := range cs {
		opt := categoryOption{ID: c.ID, Title: c.Title, Checked: checked[c.ID]}
		p.Categories = append(p.Categories, opt)
		if opt.Checked {
			p.In = append(p.In, opt)
		}
	}
	return p, nil
}

// formIDs는 폼에서 같은 이름으로 보낸 번호를 모두 읽는다. 번호가 아닌 값은 버린다.
func formIDs(r *http.Request, name string) []int64 {
	var ids []int64
	for _, v := range r.PostForm[name] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Server) saveBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	err := s.store.SetBookmark(r.Context(), userID(r), id, formIDs(r, "category"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.fail(w, r, err)
	default:
		http.Redirect(w, r, fmt.Sprintf("/books/%d?done=bookmark-saved#bookmark", id), http.StatusSeeOther)
	}
}

// reorderBookmarks는 내 북마크 화면에서 끌어서 바꾼 순서를 저장한다. 화면의 스크립트(app.js)가 부른다.
func (s *Server) reorderBookmarks(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	if err := s.store.ReorderBookmarks(r.Context(), userID(r), formIDs(r, "book")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteBookmark(r.Context(), userID(r), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/books/%d?done=bookmark-deleted#bookmark", id), http.StatusSeeOther)
}

// bookmarkCard는 내 북마크 화면의 책과, 그 책을 넣은 카테고리의 제목이다.
type bookmarkCard struct {
	Book       book.Book
	Categories []string
}

func (s *Server) renderMe(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	uid := userID(r)
	cs, err := s.store.Categories(r.Context(), uid)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	marks, err := s.store.Bookmarks(r.Context(), uid)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	titles := map[int64]string{}
	for _, c := range cs {
		titles[c.ID] = c.Title
	}
	cards := make([]bookmarkCard, len(marks))
	for i, m := range marks {
		cards[i] = bookmarkCard{Book: m.Book}
		for _, id := range m.Categories {
			cards[i].Categories = append(cards[i].Categories, titles[id])
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	s.render(w, r, "me", "layout", map[string]any{
		"Title": "내 북마크", "Nav": "me", "Categories": cs, "Bookmarks": cards, "SiteURL": s.siteURL,
		"MaxCategories": book.MaxCategories, "Error": errMsg,
	})
}

// parseForm은 폼을 읽는다. 상한보다 크면 잘린 값을 쓰지 않도록 오류 화면을 그리고 거짓을 돌려준다.
func (s *Server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		s.status(w, r, http.StatusRequestEntityTooLarge, "요청을 처리할 수 없습니다", "입력이 너무 깁니다.")
		return false
	}
	return true
}

func (s *Server) addCategory(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	c, err := book.CleanCategory(book.Category{Title: r.PostFormValue("title")})
	if err != nil {
		s.renderMe(w, r, http.StatusBadRequest, err.Error()+".")
		return
	}
	id, err := s.store.AddCategory(r.Context(), userID(r), c)
	switch {
	case errors.Is(err, store.ErrTooMany):
		s.renderMe(w, r, http.StatusConflict, fmt.Sprintf("카테고리는 %d개까지 만들 수 있습니다.", book.MaxCategories))
	case err != nil:
		s.fail(w, r, err)
	default:
		done(w, r, fmt.Sprintf("/me/categories/%d", id), "category-added")
	}
}

// category는 경로의 내 카테고리를 읽는다. 없거나 다른 사용자의 것이면 404 화면을 그리고 ok가 거짓이다.
func (s *Server) category(w http.ResponseWriter, r *http.Request) (book.Category, bool) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return book.Category{}, false
	}
	c, err := s.store.Category(r.Context(), userID(r), id)
	if errors.Is(err, store.ErrNotFound) {
		s.status(w, r, http.StatusNotFound, "찾을 수 없습니다", "찾는 카테고리가 없습니다.")
		return book.Category{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return book.Category{}, false
	}
	return c, true
}

func (s *Server) myCategory(w http.ResponseWriter, r *http.Request) {
	c, ok := s.category(w, r)
	if !ok {
		return
	}
	s.renderCategory(w, r, http.StatusOK, c, c, "")
}

// renderCategory는 카테고리 편집 화면을 그린다. form은 입력한 값이고, 입력에 오류가 있으면 그대로 두고 오류를 보인다.
func (s *Server) renderCategory(w http.ResponseWriter, r *http.Request, code int, stored, form book.Category, errMsg string) {
	books, err := s.store.CategoryBooks(r.Context(), stored.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	s.render(w, r, "category", "layout", map[string]any{
		"Title": stored.Title, "Nav": "me", "Stored": stored, "Form": form, "Books": books,
		"SiteURL": s.siteURL, "Error": errMsg,
	})
}

func (s *Server) saveCategory(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	stored, ok := s.category(w, r)
	if !ok {
		return
	}
	form := book.Category{
		ID: stored.ID, Code: stored.Code, Title: r.PostFormValue("title"), Body: r.PostFormValue("body"),
		PublicTitle: r.PostFormValue("public_title"), Public: r.PostFormValue("public") == "1",
	}
	c, err := book.CleanCategory(form)
	if err != nil {
		s.renderCategory(w, r, http.StatusBadRequest, stored, form, err.Error()+".")
		return
	}
	err = s.store.UpdateCategory(r.Context(), userID(r), c)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.fail(w, r, err)
	default:
		done(w, r, fmt.Sprintf("/me/categories/%d", c.ID), "category-saved")
	}
}

// moveCategory는 내 카테고리를 한 칸 앞이나 뒤로 옮기고, 옮긴 카테고리가 보이게 돌아간다.
func (s *Server) moveCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := formID(r, "category")
	if !ok {
		s.notFound(w, r)
		return
	}
	err := s.store.MoveCategory(r.Context(), userID(r), id, r.PostFormValue("dir") == "up")
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.fail(w, r, err)
	default:
		http.Redirect(w, r, fmt.Sprintf("/me#category-%d", id), http.StatusSeeOther)
	}
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteCategory(r.Context(), userID(r), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	done(w, r, "/me", "category-deleted")
}

// deleteAccount는 사용자와 그 북마크, 카테고리를 모두 지우고 로그아웃한다.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUser(r.Context(), userID(r)); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	s.sessions.Clear(w)
	// 지운 계정으로 로그인한 것처럼 그리지 않게 요청의 계정을 비운다.
	ctx := context.WithValue(context.WithValue(r.Context(), userKey{}, int64(0)), adminKey{}, "")
	s.status(w, r.WithContext(ctx), http.StatusOK, "계정을 지웠습니다", "북마크와 카테고리를 모두 지웠습니다. 다시 로그인하면 새 계정으로 시작합니다.")
}

// sharedList는 모든 사용자의 공개한 카테고리 목록이다. 사용자 로그인을 쓰지 않으면 없다.
func (s *Server) sharedList(w http.ResponseWriter, r *http.Request) {
	if s.subjects == nil {
		s.notFound(w, r)
		return
	}
	cs, err := s.store.PublicCategories(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "shared-list", "layout", map[string]any{"Title": "공개 북마크", "Nav": "shared", "Categories": cs})
}

// shared는 공개한 카테고리 화면이다. 공개하지 않은 카테고리는 만든 사람에게만 보인다.
// 코드는 소문자이므로 대문자로 적은 주소도 찾는다.
func (s *Server) shared(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(r.PathValue("code"))
	if !book.ValidCode(code) {
		s.sharedNotFound(w, r)
		return
	}
	c, books, err := s.store.CategoryByCode(r.Context(), code)
	if errors.Is(err, store.ErrNotFound) {
		s.sharedNotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	owner := userID(r) != 0 && userID(r) == c.UserID
	if !c.Public && !owner {
		s.sharedNotFound(w, r)
		return
	}
	s.render(w, r, "shared", "layout", map[string]any{
		"Title": c.ShownTitle(), "Nav": "shared", "Category": c, "Books": books, "Owner": owner,
	})
}

func (s *Server) sharedNotFound(w http.ResponseWriter, r *http.Request) {
	s.status(w, r, http.StatusNotFound, "찾을 수 없습니다", "공개한 카테고리가 없습니다. 주소를 확인하거나 만든 사람에게 공개 여부를 물어보세요.")
}
