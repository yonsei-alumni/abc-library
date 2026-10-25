// Package kyobo는 교보문고 검색 결과에서 책의 판본 정보를 읽고, 상품과 표지 이미지의 주소를 만든다.
// 교보문고 robots.txt는 검색과 상품 페이지 접근을 허용한다(/api/ 경로만 금지).
// 요청은 새로 등록되는 책마다 몇 번으로 제한하고 결과는 DB에 저장해서 다시 묻지 않는다.
package kyobo

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

const userAgent = "abc-library/0.1 (+https://github.com/yonsei-alumni/abc-library)"

// Edition은 교보문고 검색 결과의 판본 하나다.
type Edition struct {
	PID       string   `json:"-"`
	ISBN13    string   `json:"isbn13"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	Publisher string   `json:"publisher"`
	PubDate   string   `json:"pub_date"`
	Category  string   `json:"category"`
}

// Client는 교보문고 통합 검색을 호출한다. 요청 사이에 MinInterval만큼 쉰다.
type Client struct {
	HTTP        *http.Client
	BaseURL     string
	MinInterval time.Duration

	mu   sync.Mutex
	last time.Time
}

// NewClient는 기본 설정의 Client를 만든다.
func NewClient() *Client {
	return &Client{
		HTTP:        &http.Client{Timeout: 15 * time.Second},
		BaseURL:     "https://search.kyobobook.co.kr/search",
		MinInterval: 2 * time.Second,
	}
}

// Search는 검색어로 국내외 도서 판본을 찾는다. ISBN-13이 없는 상품(eBook 등)은 뺀다.
func (c *Client) Search(ctx context.Context, query string) ([]Edition, error) {
	c.wait()
	q := url.Values{"keyword": {query}, "gbCode": {"TOT"}, "target": {"total"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("교보문고 검색 요청 실패: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("교보문고 검색 응답 코드 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	return ParseSearch(string(body)), nil
}

func (c *Client) wait() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.MinInterval - time.Since(c.last); d > 0 {
		time.Sleep(d)
	}
	c.last = time.Now()
}

var (
	itemStart = regexp.MustCompile(`<li class="prod_item"`)
	comment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	pidRe     = regexp.MustCompile(`data-pid="([A-Z]\d+)"`)
	bidRe     = regexp.MustCompile(`data-bid="(\d+)"`)
	titleRe   = regexp.MustCompile(`<span id="cmdtName_[A-Z]\d+">([^<]+)</span>`)
	catRe     = regexp.MustCompile(`<span class="prod_category">\[([^\]]+)\]</span>`)
	authorRe  = regexp.MustCompile(`class="author[^"]*">([^<]+)</a>\s*<span class="type">([^<]+)</span>`)
	pubRe     = regexp.MustCompile(`pbcm[^>]*>([^<]+)</a>`)
	dateRe    = regexp.MustCompile(`<span class="date">\s*([^<]+?)\s*</span>`)
)

// ParseSearch는 검색 결과 HTML에서 판본 목록을 읽는다.
func ParseSearch(page string) []Edition {
	starts := itemStart.FindAllStringIndex(page, -1)
	var eds []Edition
	seen := map[string]bool{}
	for i, s := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		// 저자 이름과 역할 사이에 HTML 주석이 끼어 있어서 주석을 먼저 지운다.
		item := comment.ReplaceAllString(page[s[0]:end], "")
		ed := Edition{
			PID:       first(pidRe, item),
			ISBN13:    first(bidRe, item),
			Title:     first(titleRe, item),
			Category:  first(catRe, item),
			Publisher: first(pubRe, item),
			PubDate:   first(dateRe, item),
		}
		for _, m := range authorRe.FindAllStringSubmatch(item, -1) {
			if isAuthorRole(m[2]) {
				ed.Authors = append(ed.Authors, strings.TrimSpace(html.UnescapeString(m[1])))
			}
		}
		if !book.ValidISBN13(ed.ISBN13) || ed.Title == "" || seen[ed.ISBN13] {
			continue
		}
		seen[ed.ISBN13] = true
		eds = append(eds, ed)
	}
	return eds
}

func first(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(m[1]))
}

// isAuthorRole은 번역, 그림, 감수 같은 역할을 빼고 글쓴이 역할만 고른다.
func isAuthorRole(role string) bool {
	for _, r := range []string{"저", "글", "지은", "엮", "편"} {
		if strings.Contains(role, r) {
			return !strings.Contains(role, "번역")
		}
	}
	return false
}

var productPath = regexp.MustCompile(`^/detail/([A-Z]\d+)/?$`)

// ProductPID는 교보문고 상품 페이지 주소(https://product.kyobobook.co.kr/detail/S000001981838)의 상품 번호다.
// 상품 페이지 주소가 아니면 ok가 거짓이다.
func ProductPID(raw string) (pid string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Hostname(), "product.kyobobook.co.kr") {
		return "", false
	}
	m := productPath.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ProductURL은 교보문고 상품 페이지 주소다.
func ProductURL(pid string) string {
	return "https://product.kyobobook.co.kr/detail/" + url.PathEscape(pid)
}

// SearchURL은 상품을 확정하지 못한 책을 교보문고에서 찾아보는 검색 주소다.
func SearchURL(query string) string {
	return "https://search.kyobobook.co.kr/search?" + url.Values{"keyword": {query}}.Encode()
}

// CoverURL은 교보문고가 ISBN으로 제공하는 표지 이미지 주소다. width는 픽셀 단위 너비다.
func CoverURL(isbn13 string, width int) string {
	return fmt.Sprintf("https://contents.kyobobook.co.kr/sih/fit-in/%dx0/pdt/%s.jpg", width, url.PathEscape(isbn13))
}
