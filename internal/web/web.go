// Package web은 책과 언급 요약을 보여 주는 HTTP 서버다. 화면은 html/template으로 그리고 상호작용은 HTMX로 처리한다.
package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/band"
	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/kyobo"
	"github.com/yonsei-alumni/abc-library/internal/store"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static
var staticFiles embed.FS

const pageSize = 24

// Store는 화면에 필요한 조회다. *store.Store가 구현한다.
type Store interface {
	Timeline(ctx context.Context, limit, offset int) ([]book.Entry, error)
	Books(ctx context.Context, f store.Filter, limit, offset int) ([]book.Shelved, error)
	Book(ctx context.Context, id int64) (book.Book, []book.Mention, error)
	TagCounts(ctx context.Context) ([]book.Count, error)
	AuthorCounts(ctx context.Context) ([]book.Count, error)
}

// Server는 웹 화면이다.
type Server struct {
	store  Store
	bandNo string
	log    *slog.Logger
	pages  map[string]*template.Template
}

// New는 템플릿을 읽어 Server를 만든다.
func New(st Store, bandNo string, log *slog.Logger) (*Server, error) {
	s := &Server{store: st, bandNo: bandNo, log: log, pages: map[string]*template.Template{}}
	funcs := template.FuncMap{
		"date":     func(t time.Time) string { return t.In(seoul).Format("2006년 1월 2일") },
		"isoDate":  func(t time.Time) string { return t.In(seoul).Format("2006-01-02") },
		"cover":    func(b book.Book) string { return coverURL(b, 240) },
		"coverBig": func(b book.Book) string { return coverURL(b, 458) },
		"buyURL":   buyURL,
		"postURL":  func(no int) string { return band.PostURL(s.bandNo, no) },
		"authors":  func(as []string) string { return strings.Join(as, ", ") },
		"isTopic":  func(tag string) bool { return slices.Contains(book.TopicTags, tag) },
		"static":   staticURL,
	}
	for _, page := range []string{"timeline", "books", "book", "tags", "authors", "error"} {
		t, err := template.New("").Funcs(funcs).ParseFS(templateFiles, "templates/layout.html", "templates/parts.html", "templates/"+page+".html")
		if err != nil {
			return nil, err
		}
		s.pages[page] = t
	}
	return s, nil
}

var seoul = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		return time.FixedZone("KST", 9*60*60)
	}
	return loc
}()

// staticURL은 정적 파일 주소에 내용 해시를 붙인다. 파일이 바뀌면 주소가 바뀌어서 브라우저 캐시를 피한다.
func staticURL(name string) string {
	data, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		return "/static/" + name
	}
	sum := sha256.Sum256(data)
	return "/static/" + name + "?v=" + hex.EncodeToString(sum[:4])
}

func coverURL(b book.Book, width int) string {
	if b.ISBN13 == "" {
		return ""
	}
	return kyobo.CoverURL(b.ISBN13, width)
}

// buyURL은 교보문고 상품 페이지 주소다. 상품을 확정하지 못한 책은 교보문고 검색 주소를 쓴다.
func buyURL(b book.Book) string {
	if b.KyoboPID != "" {
		return kyobo.ProductURL(b.KyoboPID)
	}
	q := b.Title
	if len(b.Authors) > 0 {
		q += " " + b.Authors[0]
	}
	return kyobo.SearchURL(q)
}

// Handler는 라우팅을 설정한 http.Handler다.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.timeline)
	mux.HandleFunc("GET /books", s.books)
	mux.HandleFunc("GET /books/{id}", s.book)
	mux.HandleFunc("GET /tags", s.tags)
	mux.HandleFunc("GET /authors", s.authors)
	// Cloud Run 앞단의 Google Frontend가 정확히 /healthz만 가로채서 자체 404를 돌려주므로 /health도 연다.
	health := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /health", health)
	static := http.FileServerFS(staticFiles)
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		static.ServeHTTP(w, r)
	}))
	// 비공개 밴드에서 나온 요약을 싣는 사이트라서 검색엔진이 수집하지 않게 한다.
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		mux.ServeHTTP(w, r)
	})
}

func page(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || p < 1 {
		return 1
	}
	return p
}

// isMore는 HTMX의 '더 보기' 요청인지 알려 준다. 이때는 목록 조각만 그린다.
func isMore(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && strings.HasSuffix(r.Header.Get("HX-Target"), "-more")
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	p := page(r)
	entries, err := s.store.Timeline(r.Context(), pageSize+1, (p-1)*pageSize)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	more := len(entries) > pageSize
	if more {
		entries = entries[:pageSize]
	}
	data := map[string]any{"Title": "최근 소식", "Nav": "timeline", "Entries": entries, "NextPage": nextPage(more, p)}
	if isMore(r) {
		s.render(w, r, "timeline", "timeline-items", data)
		return
	}
	s.render(w, r, "timeline", "layout", data)
}

func (s *Server) books(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.Filter{Tag: q.Get("tag"), Author: q.Get("author"), Query: strings.TrimSpace(q.Get("q"))}
	p := page(r)
	shelf, err := s.store.Books(r.Context(), f, pageSize+1, (p-1)*pageSize)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	more := len(shelf) > pageSize
	if more {
		shelf = shelf[:pageSize]
	}
	title := "모든 책"
	switch {
	case f.Query != "":
		title = "‘" + f.Query + "’ 검색 결과"
	case f.Tag != "":
		title = "#" + f.Tag
	case f.Author != "":
		title = f.Author + "의 책"
	}
	data := map[string]any{"Title": title, "Nav": "books", "Filter": f, "Query": f.Query, "Shelf": shelf, "NextPage": nextPage(more, p)}
	if isMore(r) {
		s.render(w, r, "books", "book-cards", data)
		return
	}
	s.render(w, r, "books", "layout", data)
}

func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	b, mentions, err := s.store.Book(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "book", "layout", map[string]any{
		"Title": b.Title, "Nav": "books", "Book": b, "Count": len(mentions), "Threads": book.Threads(mentions),
	})
}

func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.TagCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// 모임 주제는 정한 순서(최근 주제가 먼저)로 앞에 두고, 나머지 태그는 책이 많은 순서로 둔다.
	var topics, others []book.Count
	for _, topic := range book.TopicTags {
		if i := slices.IndexFunc(counts, func(c book.Count) bool { return c.Name == topic }); i >= 0 {
			topics = append(topics, counts[i])
		}
	}
	for _, c := range counts {
		if !slices.Contains(book.TopicTags, c.Name) {
			others = append(others, c)
		}
	}
	s.render(w, r, "tags", "layout", map[string]any{"Title": "태그", "Nav": "tags", "Topics": topics, "Others": others})
}

func (s *Server) authors(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.AuthorCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "authors", "layout", map[string]any{"Title": "저자", "Nav": "authors", "Counts": counts})
}

func nextPage(more bool, p int) int {
	if more {
		return p + 1
	}
	return 0
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[page].ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("화면을 그리지 못했다", "path", r.URL.Path, "오류", err)
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "error", "layout", map[string]any{"Title": "찾을 수 없습니다", "Message": "찾는 책이 없습니다."})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("요청 처리 실패", "path", r.URL.Path, "오류", err)
	w.WriteHeader(http.StatusInternalServerError)
	s.render(w, r, "error", "layout", map[string]any{"Title": "오류", "Message": "잠시 후 다시 시도해 주세요."})
}
