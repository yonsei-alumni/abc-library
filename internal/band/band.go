// Package band는 Playwright로 밴드 웹 화면을 열어 게시글을 읽는다.
// 로그인은 사람이 직접 하고, 코드는 저장한 storageState만 쓴다. 글쓰기 같은 쓰기 동작은 하지 않는다.
// 밴드 내부 API를 직접 호출하지 않고, 웹 클라이언트가 그린 화면에서 내용을 읽는다.
package band

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

const (
	maxImages    = 4
	maxImageSize = 4 << 20
)

var (
	// ErrSessionExpired는 저장한 로그인 세션이 만료되었다는 뜻이다.
	ErrSessionExpired = errors.New("밴드 로그인 세션이 만료되었습니다. make band-login으로 다시 로그인하세요")
	// ErrNotFound는 삭제되었거나 없는 게시글이라는 뜻이다.
	ErrNotFound = errors.New("게시글이 없습니다")
)

// Post는 밴드 게시글 하나의 내용이다. 글과 댓글의 작성자 정보는 읽지 않는다.
type Post struct {
	No       int
	PostedAt time.Time
	Text     string
	// Links는 본문에 있는 외부 링크다. 댓글의 링크는 Comment에 따로 둔다.
	Links    []book.Link
	Images   []Image
	Comments []Comment
}

// Comment는 게시글의 댓글 하나다.
type Comment struct {
	At    time.Time
	Text  string
	Links []book.Link
}

// Image는 게시글에 첨부된 사진이다.
type Image struct {
	MediaType string
	Data      []byte
}

// PostURL은 게시글 주소다. 비공개 밴드라서 멤버만 열어 볼 수 있다.
func PostURL(bandNo string, postNo int) string {
	return fmt.Sprintf("https://band.us/band/%s/post/%d", bandNo, postNo)
}

// OnPostPage는 브라우저가 아직 그 게시글 화면에 있는지 알려 준다. 밴드는 band.us와 www.band.us를 오간다.
func OnPostPage(pageURL, bandNo string, postNo int) bool {
	u, err := url.Parse(pageURL)
	if err != nil || (u.Host != "band.us" && u.Host != "www.band.us") {
		return false
	}
	return strings.TrimSuffix(u.Path, "/") == fmt.Sprintf("/band/%s/post/%d", bandNo, postNo)
}

// InstallBrowser는 Playwright 드라이버와 Chromium이 없으면 내려받는다.
func InstallBrowser() error {
	return playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}, Verbose: false})
}

func newContext(browser playwright.Browser, statePath string) (playwright.BrowserContext, error) {
	opts := playwright.BrowserNewContextOptions{
		Locale:     playwright.String("ko-KR"),
		TimezoneId: playwright.String("Asia/Seoul"),
	}
	if statePath != "" {
		opts.StorageStatePath = playwright.String(statePath)
	}
	return browser.NewContext(opts)
}

// Login은 브라우저 창을 띄우고, 사람이 로그인을 마치면 세션을 statePath에 저장한다.
func Login(statePath string, timeout time.Duration) error {
	pw, err := playwright.Run()
	if err != nil {
		return err
	}
	defer func() { _ = pw.Stop() }()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(false)})
	if err != nil {
		return err
	}
	defer func() { _ = browser.Close() }()
	bctx, err := newContext(browser, "")
	if err != nil {
		return err
	}
	page, err := bctx.NewPage()
	if err != nil {
		return err
	}
	if _, err := page.Goto("https://band.us/home"); err != nil {
		return err
	}
	fmt.Println("브라우저 창에서 밴드에 로그인하세요. 로그인을 마치면 세션을 저장하고 창을 닫습니다.")

	deadline := time.Now().Add(timeout)
	for !loggedIn(bctx, page) {
		if time.Now().After(deadline) {
			return errors.New("로그인을 기다리다가 시간이 지났습니다")
		}
		time.Sleep(2 * time.Second)
	}
	if _, err := bctx.StorageState(playwright.BrowserContextStorageStateOptions{Path: playwright.String(statePath)}); err != nil {
		return err
	}
	return os.Chmod(statePath, 0o600)
}

func loggedIn(bctx playwright.BrowserContext, page playwright.Page) bool {
	if strings.Contains(page.URL(), "auth.band.us") {
		return false
	}
	cookies, err := bctx.Cookies("https://band.us")
	if err != nil {
		return false
	}
	for _, c := range cookies {
		if c.Name == "band_session" && c.Value != "" {
			return true
		}
	}
	return false
}

// Crawler는 저장한 세션으로 밴드 화면을 여는 헤드리스 브라우저다.
type Crawler struct {
	BandNo string
	// Delay는 페이지를 열기 전에 쉬는 시간이다. 밴드에 부담을 주지 않기 위해 둔다.
	Delay time.Duration

	pw      *playwright.Playwright
	browser playwright.Browser
	bctx    playwright.BrowserContext
	page    playwright.Page

	// dialogs는 페이지가 띄운 알림창 수다. 삭제된 글은 알림창을 띄우고 목록으로 돌아간다.
	dialogs atomic.Int32
}

// Open은 statePath의 세션으로 브라우저를 연다.
func Open(statePath, bandNo string) (*Crawler, error) {
	if _, err := os.Stat(statePath); err != nil {
		return nil, fmt.Errorf("세션 파일 %s를 읽을 수 없습니다. make band-login으로 먼저 로그인하세요: %w", statePath, err)
	}
	c := &Crawler{BandNo: bandNo, Delay: 2 * time.Second}
	var err error
	if c.pw, err = playwright.Run(); err != nil {
		return nil, err
	}
	if c.browser, err = c.pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)}); err != nil {
		_ = c.Close()
		return nil, err
	}
	if c.bctx, err = newContext(c.browser, statePath); err != nil {
		_ = c.Close()
		return nil, err
	}
	if c.page, err = c.bctx.NewPage(); err != nil {
		_ = c.Close()
		return nil, err
	}
	c.page.OnDialog(func(d playwright.Dialog) {
		c.dialogs.Add(1)
		_ = d.Dismiss()
	})
	return c, nil
}

// Close는 브라우저를 닫는다.
func (c *Crawler) Close() error {
	if c.browser != nil {
		_ = c.browser.Close()
	}
	if c.pw != nil {
		return c.pw.Stop()
	}
	return nil
}

func (c *Crawler) open(url string) error {
	time.Sleep(c.Delay)
	if _, err := c.page.Goto(url, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateDomcontentloaded}); err != nil {
		return err
	}
	if strings.Contains(c.page.URL(), "auth.band.us") || strings.Contains(c.page.URL(), "/login") {
		return ErrSessionExpired
	}
	return nil
}

// LatestPostNo는 게시글 목록에서 가장 최근 글의 번호를 읽는다.
func (c *Crawler) LatestPostNo() (int, error) {
	if err := c.open(fmt.Sprintf("https://band.us/band/%s/post", c.BandNo)); err != nil {
		return 0, err
	}
	sel := fmt.Sprintf(`a[href*="/band/%s/post/"]`, c.BandNo)
	if err := c.page.Locator(sel).First().WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached, Timeout: playwright.Float(20000),
	}); err != nil {
		return 0, fmt.Errorf("게시글 목록을 읽지 못했습니다: %w", err)
	}
	v, err := c.page.Evaluate(`bandNo => Math.max(0, ...[...document.querySelectorAll('a[href*="/band/' + bandNo + '/post/"]')]
		.map(a => Number((a.getAttribute('href').match(/\/post\/(\d+)/) || [])[1] || 0)))`, c.BandNo)
	if err != nil {
		return 0, err
	}
	n, ok := v.(int)
	if !ok {
		return 0, fmt.Errorf("게시글 번호를 읽지 못했습니다: %v", v)
	}
	return n, nil
}

// extractScript는 게시글 상세 화면에서 본문, 외부 링크, 첨부 사진, 게시 시각과 댓글을 읽는다.
// 글과 댓글의 작성자 영역은 읽지 않는다.
const extractScript = `() => {
  const main = document.querySelector('.postWrap .postMain');
  if (!main) return null;
  // 본문의 주소 텍스트와 링크 미리보기가 같은 주소를 가리키므로, 주소마다 하나만 남기고 미리보기 제목을 쓴다.
  const linksIn = (root) => {
    const links = new Map();
    for (const a of root ? root.querySelectorAll('a[href^="http"]') : []) {
      const href = a.getAttribute('href');
      if (/^https?:\/\/([^\/]*\.)?band\.us(\/|$)/.test(href)) continue;
      const snippet = a.querySelector('.snippetTit');
      let title = (snippet ? snippet.textContent : a.textContent).trim();
      if (title === href) title = '';
      const prev = links.get(href);
      if (!prev || (!prev.title && title)) links.set(href, {url: href, title});
    }
    return [...links.values()];
  };
  const body = main.querySelector('.postBody');
  const text = body && body.querySelector('.dPostTextView .txtBody');
  const images = body ? [...body.querySelectorAll('img')]
    .filter(img => !img.closest('.snippet'))
    .map(img => img.currentSrc || img.getAttribute('src'))
    .filter(src => src && /^https:\/\/[^\/]*pstatic\.net\//.test(src)) : [];
  // 최근 글과 댓글은 화면에 '17시간 전'처럼 나오므로, 절대 시각이 담긴 title 속성을 먼저 읽는다.
  const timeOf = (el) => el ? (el.getAttribute('title') || el.textContent).trim() : '';
  const time = main.querySelector('.postWriterInfoWrap time.time');
  const comments = [...document.querySelectorAll('.postWrap .dPostCommentMainView .cComment')].map(c => {
    const cbody = c.querySelector('.commentBody');
    const ctext = cbody && cbody.querySelector('p.txt');
    const ctime = c.querySelector('.func time.time');
    return {text: ctext ? ctext.innerText.trim() : '', time: timeOf(ctime), links: linksIn(cbody)};
  }).filter(c => c.text || c.links.length);
  return JSON.stringify({
    text: text ? text.innerText : '', links: linksIn(body), images: [...new Set(images)],
    time: timeOf(time), comments,
  });
}`

type scraped struct {
	Text     string      `json:"text"`
	Links    []book.Link `json:"links"`
	Images   []string    `json:"images"`
	Time     string      `json:"time"`
	Comments []struct {
		Text  string      `json:"text"`
		Time  string      `json:"time"`
		Links []book.Link `json:"links"`
	} `json:"comments"`
}

var moreComments = regexp.MustCompile(`이전 댓글|댓글 더보기`)

// loadAllComments는 접혀 있는 이전 댓글을 펼친다. 댓글 목록을 더 불러오기만 하고 쓰기 동작은 하지 않는다.
func (c *Crawler) loadAllComments() {
	for range 10 {
		btn := c.page.Locator(".dPostCommentMainView").Locator("button, a", playwright.LocatorLocatorOptions{HasText: moreComments}).First()
		if n, err := btn.Count(); err != nil || n == 0 {
			return
		}
		if err := btn.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(5000)}); err != nil {
			return
		}
		time.Sleep(time.Second)
	}
}

// Post는 게시글 하나를 읽는다. 삭제된 글이면 ErrNotFound를 돌려준다.
func (c *Crawler) Post(no int) (Post, error) {
	c.dialogs.Store(0)
	if err := c.open(PostURL(c.BandNo, no)); err != nil {
		return Post{}, err
	}
	err := c.page.Locator(".postWrap .postMain .postBody").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateAttached, Timeout: playwright.Float(15000),
	})
	if err != nil {
		return Post{}, ErrNotFound
	}
	c.loadAllComments()
	v, err := c.page.Evaluate(extractScript)
	if err != nil {
		return Post{}, err
	}
	// 삭제된 글은 '삭제되었거나 찾을 수 없습니다' 알림창을 띄우고 게시글 목록으로 옮겨 간다.
	// 목록 화면에도 같은 구조의 글이 있으므로, 그 글을 이 번호의 글로 읽지 않게 확인한다.
	if c.dialogs.Load() > 0 || !OnPostPage(c.page.URL(), c.BandNo, no) {
		return Post{}, ErrNotFound
	}
	raw, ok := v.(string)
	if !ok {
		return Post{}, ErrNotFound
	}
	var s scraped
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Post{}, err
	}
	postedAt, err := ParseTime(s.Time, time.Now())
	if err != nil {
		return Post{}, fmt.Errorf("게시글 %d: %w", no, err)
	}
	p := Post{No: no, PostedAt: postedAt, Text: s.Text, Links: s.Links}
	for _, sc := range s.Comments {
		at, err := ParseTime(sc.Time, time.Now())
		if err != nil || at.Before(postedAt) {
			at = postedAt
		}
		p.Comments = append(p.Comments, Comment{At: at, Text: sc.Text, Links: sc.Links})
	}
	for _, src := range s.Images {
		if len(p.Images) == maxImages {
			break
		}
		if img, err := c.download(src); err == nil {
			p.Images = append(p.Images, img)
		}
	}
	return p, nil
}

func (c *Crawler) download(src string) (Image, error) {
	resp, err := c.bctx.Request().Get(src, playwright.APIRequestContextGetOptions{
		Headers: map[string]string{"Referer": "https://band.us/"},
	})
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = resp.Dispose() }()
	if resp.Status() != http.StatusOK {
		return Image{}, fmt.Errorf("사진 응답 코드 %d", resp.Status())
	}
	data, err := resp.Body()
	if err != nil {
		return Image{}, err
	}
	mediaType := http.DetectContentType(data)
	switch {
	case len(data) > maxImageSize:
		return Image{}, errors.New("사진이 너무 큽니다")
	case mediaType != "image/jpeg" && mediaType != "image/png" && mediaType != "image/gif" && mediaType != "image/webp":
		return Image{}, fmt.Errorf("지원하지 않는 사진 형식 %s", mediaType)
	}
	return Image{MediaType: mediaType, Data: data}, nil
}

var timeRe = regexp.MustCompile(`(?:(\d{4})년\s*)?(\d{1,2})월\s*(\d{1,2})일\s*(오전|오후)\s*(\d{1,2}):(\d{2})`)

var seoul = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		return time.FixedZone("KST", 9*60*60)
	}
	return loc
}()

// ParseTime은 "2026년 10월 23일 오후 8:09" 형식의 게시 시각을 읽는다.
// 올해 글처럼 연도가 빠져 있으면 now의 연도로 보고, 그 시각이 미래면 작년으로 본다.
func ParseTime(s string, now time.Time) (time.Time, error) {
	m := timeRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, fmt.Errorf("게시 시각 %q를 읽지 못했습니다", s)
	}
	now = now.In(seoul)
	year := now.Year()
	if m[1] != "" {
		year, _ = strconv.Atoi(m[1])
	}
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[5])
	minute, _ := strconv.Atoi(m[6])
	if hour == 12 {
		hour = 0
	}
	if m[4] == "오후" {
		hour += 12
	}
	t := time.Date(year, time.Month(month), day, hour, minute, 0, 0, seoul)
	if m[1] == "" && t.After(now) {
		t = t.AddDate(-1, 0, 0)
	}
	return t, nil
}
