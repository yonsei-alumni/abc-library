// Package weekly는 카카오톡 대화방의 한 주 대화를 Claude로 정리해서 밴드에 붙여 넣을 '대화방 주간 요약'을 만든다.
// Claude에게는 메시지 본문과 보낸 시각만 보내고 작성자는 보내지 않는다.
// 링크는 코드가 본문에서 모두 뽑아서 책이나 링크 모음에 하나씩 넣으므로 요약에서 빠지지 않는다.
// 책의 판본은 extract와 같이 Claude가 search_kyobo 도구로 고르고, 코드는 검색 결과나 상품 링크에 있었던 판본만 받아들인다.
package weekly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/extract"
	"github.com/yonsei-alumni/abc-library/internal/kakao"
	"github.com/yonsei-alumni/abc-library/internal/kyobo"
)

const (
	model         = "claude-opus-5-5"
	maxTurns      = 12
	maxCandidates = 8
	// maxLinkedProducts는 상품 번호로 찾아보는 교보문고 상품 링크 수의 상한이다.
	maxLinkedProducts = 8
	// maxMessageRunes는 메시지 하나에서 Claude에게 보내는 글자 수의 상한이다.
	maxMessageRunes = 3000
	keywordCount    = 10
)

// ErrRefused는 Claude가 요약을 거절했다는 뜻이다.
var ErrRefused = errors.New("claude가 대화방 요약을 거절했습니다")

// BookFinder는 사이트에 있는 책을 찾는다. *store.Store가 구현한다.
type BookFinder interface {
	FindBook(ctx context.Context, b book.Book) (id int64, ok bool, err error)
}

// Summarizer는 대화방 주간 요약을 만든다.
type Summarizer struct {
	api    extract.MessageAPI
	search extract.Searcher
	titles TitleFetcher
	books  BookFinder
	site   string
}

// New는 Summarizer를 만든다. titles가 nil이면 링크 제목을 읽지 않고, books가 nil이면 사이트의 책 링크를 붙이지 않는다.
// siteURL은 책 상세 화면 주소의 앞부분(https://abc-lib.web.app)이다.
func New(api extract.MessageAPI, search extract.Searcher, titles TitleFetcher, books BookFinder, siteURL string) *Summarizer {
	return &Summarizer{api: api, search: search, titles: titles, books: books, site: strings.TrimRight(siteURL, "/")}
}

// Input은 요약할 기간과 메시지다. 메시지는 kakao.Week로 거르고 정렬한 것이다.
type Input struct {
	Room     string
	Start    string
	End      string
	Messages []kakao.Message
}

// Summary는 정리한 결과다. Text가 밴드에 붙여 넣을 글을 만든다.
type Summary struct {
	Room     string
	Start    string
	End      string
	Count    int
	Books    []Book
	Links    []SharedLink
	Keywords []string
}

// Book은 대화에서 공유되거나 추천된 책이다.
type Book struct {
	Title   string
	Authors []string
	Context []string
	// Links는 이 책과 관련된 링크 중 서점 링크를 뺀 것이다. 구매 링크는 교보문고로 통일한다.
	Links []Link
	// Purchase는 교보문고 상품 주소다. 상품을 확정하지 못하면 교보문고 검색 주소이고 Searched가 참이다.
	Purchase string
	Searched bool
	// Page는 사이트의 책 상세 화면 주소다. 사이트에 없는 책이면 비어 있다.
	Page string
}

// SharedLink는 어느 책에도 넣지 않은 링크다.
type SharedLink struct {
	Link
	Context string
}

type searchInput struct {
	Query string `json:"query"`
}

type result struct {
	Books []struct {
		Title    string   `json:"title"`
		Authors  []string `json:"authors"`
		ISBN13   string   `json:"isbn13"`
		KyoboPID string   `json:"kyobo_pid"`
		Context  []string `json:"context"`
		Links    []int    `json:"links"`
	} `json:"books"`
	Links []struct {
		Link    int    `json:"link"`
		Title   string `json:"title"`
		Context string `json:"context"`
	} `json:"links"`
	Keywords []string `json:"keywords"`
}

// Summarize는 한 주의 메시지를 정리한다. 메시지가 없으면 Claude를 부르지 않는다.
func (s *Summarizer) Summarize(ctx context.Context, in Input) (Summary, error) {
	sum := Summary{Room: in.Room, Start: in.Start, End: in.End, Count: len(in.Messages)}
	if len(in.Messages) == 0 {
		return sum, nil
	}
	links := fetchTitles(ctx, s.titles, ExtractURLs(in.Messages))
	linked := s.linkedEditions(ctx, links)
	seen := map[string]kyobo.Edition{}
	for _, ed := range linked {
		if ed.ISBN13 != "" {
			seen[ed.ISBN13] = ed
		}
	}
	messages := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userText(in, links, linked)))}
	for range maxTurns {
		resp, err := s.api.New(ctx, params(messages))
		if err != nil {
			return sum, err
		}
		switch resp.StopReason {
		case anthropic.BetaStopReasonRefusal:
			return sum, ErrRefused
		case anthropic.BetaStopReasonToolUse:
			messages = append(messages, resp.ToParam())
			var results []anthropic.BetaContentBlockParamUnion
			for _, block := range resp.Content {
				if use, ok := block.AsAny().(anthropic.BetaToolUseBlock); ok {
					results = append(results, s.runSearch(ctx, use, seen))
				}
			}
			messages = append(messages, anthropic.NewBetaUserMessage(results...))
		case anthropic.BetaStopReasonEndTurn:
			var r result
			if err := json.Unmarshal([]byte(responseText(resp)), &r); err != nil {
				return sum, fmt.Errorf("요약 결과를 읽지 못했습니다: %w", err)
			}
			s.fill(ctx, &sum, r, links, seen, linked)
			return sum, nil
		default:
			return sum, fmt.Errorf("예상하지 못한 stop_reason: %s", resp.StopReason)
		}
	}
	return sum, fmt.Errorf("도구 호출이 %d번을 넘었습니다", maxTurns)
}

func params(messages []anthropic.BetaMessageParam) anthropic.BetaMessageNewParams {
	return anthropic.BetaMessageNewParams{
		Model:     model,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt}},
		Messages:  messages,
		Tools: []anthropic.BetaToolUnionParam{{OfTool: &anthropic.BetaToolParam{
			Name:        "search_kyobo",
			Description: anthropic.String(searchToolDescription),
			Strict:      anthropic.Bool(true),
			InputSchema: anthropic.BetaToolInputSchemaParam{
				Properties: map[string]any{
					"query": map[string]any{"type": "string", "description": "검색어. 제목과 저자"},
				},
				Required:    []string{"query"},
				ExtraFields: map[string]any{"additionalProperties": false},
			},
		}}},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortMedium,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: resultSchema()},
		},
		// 안전 분류기가 오탐으로 거절하면 서버가 다른 모델로 다시 처리한다.
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
	}
}

func responseText(resp *anthropic.BetaMessage) string {
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// linkedEditions는 교보문고 상품 링크가 가리키는 판본을 상품 번호로 찾는다. 결과의 키는 상품 번호다.
func (s *Summarizer) linkedEditions(ctx context.Context, links []Link) map[string]kyobo.Edition {
	editions := map[string]kyobo.Edition{}
	tried := map[string]bool{}
	for _, l := range links {
		pid, ok := kyobo.ProductPID(l.URL)
		if !ok || tried[pid] || len(tried) >= maxLinkedProducts {
			continue
		}
		tried[pid] = true
		if ed, ok, err := s.search.Product(ctx, pid); err == nil && ok {
			editions[pid] = ed
		}
	}
	return editions
}

var weekdays = []string{"일", "월", "화", "수", "목", "금", "토"}

// userText는 Claude에게 보내는 메시지와 링크 목록이다. 작성자는 넣지 않는다.
func userText(in Input, links []Link, linked map[string]kyobo.Edition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "대화방: %s\n기간: %s ~ %s\n\n## 메시지\n", in.Room, in.Start, in.End)
	for i, m := range in.Messages {
		at := m.At().In(book.Seoul)
		text := strings.TrimSpace(m.Text)
		if r := []rune(text); len(r) > maxMessageRunes {
			text = string(r[:maxMessageRunes]) + "…(생략)"
		}
		text = strings.ReplaceAll(text, "\n", "\n    ")
		fmt.Fprintf(&b, "[%d] %s(%s) %s %s\n", i+1, at.Format("01-02"), weekdays[at.Weekday()], clock(m, at), text)
	}
	b.WriteString("\n## 링크\n")
	if len(links) == 0 {
		b.WriteString("(없음)\n")
	}
	for i, l := range links {
		fmt.Fprintf(&b, "[L%d] %s", i+1, readableURL(l.URL))
		if l.Title != "" {
			fmt.Fprintf(&b, " - %s", l.Title)
		}
		if pid, ok := kyobo.ProductPID(l.URL); ok {
			if ed, ok := linked[pid]; ok {
				isbn := ed.ISBN13
				if isbn == "" {
					isbn = "없음"
				}
				fmt.Fprintf(&b, " → 교보문고 상품: 상품 번호 %s, isbn13 %s, %s, %s, %s", ed.PID, isbn, ed.Title, strings.Join(ed.Authors, ", "), ed.Publisher)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// clock은 메시지의 시각이다. 폰이 시각을 읽지 못한 메시지는 추정한 시각이므로 ~를 붙인다.
func clock(m kakao.Message, at time.Time) string {
	if m.Time != "" && m.Date != "" {
		return at.Format("15:04")
	}
	return "~" + at.Format("15:04")
}

// readableURL은 퍼센트 인코딩된 주소를 사람이 읽을 수 있게 푼다. 주소에 든 제목이 책을 알아볼 단서가 된다.
func readableURL(raw string) string {
	if s, err := url.PathUnescape(raw); err == nil {
		return s
	}
	return raw
}

func (s *Summarizer) runSearch(ctx context.Context, use anthropic.BetaToolUseBlock, seen map[string]kyobo.Edition) anthropic.BetaContentBlockParamUnion {
	var in searchInput
	if use.Name != "search_kyobo" || json.Unmarshal([]byte(use.JSON.Input.Raw()), &in) != nil || strings.TrimSpace(in.Query) == "" {
		return anthropic.NewBetaToolResultBlock(use.ID, "알 수 없는 도구 호출입니다. search_kyobo에 query를 넣어 호출하세요.", true)
	}
	eds, err := s.search.Search(ctx, in.Query)
	if err != nil {
		return anthropic.NewBetaToolResultBlock(use.ID, "검색에 실패했습니다: "+err.Error(), true)
	}
	if len(eds) > maxCandidates {
		eds = eds[:maxCandidates]
	}
	for _, ed := range eds {
		if _, ok := seen[ed.ISBN13]; !ok {
			seen[ed.ISBN13] = ed
		}
	}
	out, _ := json.Marshal(eds)
	return anthropic.NewBetaToolResultBlock(use.ID, string(out), false)
}

// fill은 Claude의 결과를 검증해서 Summary를 채운다.
//   - 상품 링크와 검색 결과에 없던 상품 번호, ISBN은 버린다.
//   - 링크 번호는 처음 나온 책 하나에만 붙이고, 어느 책에도 붙지 않은 링크는 모두 링크 모음에 넣는다.
//   - 책에 붙은 서점 링크는 교보문고 구매 링크로 대신한다.
func (s *Summarizer) fill(ctx context.Context, sum *Summary, r result, links []Link, seen, linked map[string]kyobo.Edition) {
	used := make([]bool, len(links))
	keys := map[string]bool{}
	for _, rb := range r.Books {
		b := book.Book{Title: strings.TrimSpace(rb.Title), Authors: trimAll(rb.Authors)}
		ed, ok := linked[rb.KyoboPID]
		if !ok && rb.ISBN13 != "" {
			ed, ok = seen[rb.ISBN13]
		}
		if ok {
			b.ISBN13, b.Title = ed.ISBN13, ed.Title
			if len(ed.Authors) > 0 {
				b.Authors = ed.Authors
			}
		}
		key := book.WorkKey(b.Title, b.Authors)
		if b.Title == "" || keys[key] {
			continue
		}
		keys[key] = true

		out := Book{Title: b.Title, Authors: b.Authors, Context: trimAll(rb.Context)}
		if ok && ed.PID != "" {
			out.Purchase = kyobo.ProductURL(ed.PID)
		} else {
			out.Purchase = kyobo.SearchURL(strings.TrimSpace(b.Title + " " + strings.Join(first(b.Authors), "")))
			out.Searched = true
		}
		for _, no := range rb.Links {
			if no < 1 || no > len(links) || used[no-1] {
				continue
			}
			used[no-1] = true
			if u, err := url.Parse(links[no-1].URL); err == nil && extract.IsStoreLink(u) {
				continue
			}
			out.Links = append(out.Links, links[no-1])
		}
		if s.books != nil {
			// 사이트 링크는 덤이라서 조회에 실패해도 요약은 보낸다.
			if id, found, err := s.books.FindBook(ctx, b); err == nil && found && s.site != "" {
				out.Page = fmt.Sprintf("%s/books/%d", s.site, id)
			}
		}
		sum.Books = append(sum.Books, out)
	}

	notes := map[int]SharedLink{}
	for _, rl := range r.Links {
		if _, dup := notes[rl.Link]; !dup {
			notes[rl.Link] = SharedLink{Link: Link{Title: strings.TrimSpace(rl.Title)}, Context: strings.TrimSpace(rl.Context)}
		}
	}
	for i, l := range links {
		if used[i] {
			continue
		}
		sl := notes[i+1]
		sl.URL = l.URL
		if sl.Title == "" {
			sl.Title = l.Title
		}
		sum.Links = append(sum.Links, sl)
	}

	for _, k := range trimAll(r.Keywords) {
		if !slices.Contains(sum.Keywords, k) && len(sum.Keywords) < keywordCount {
			sum.Keywords = append(sum.Keywords, k)
		}
	}
}

func first(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	return ss[:1]
}

func trimAll(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
