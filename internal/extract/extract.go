// Package extract는 Claude로 밴드 게시글과 댓글에서 책을 찾고, 개인 정보를 뺀 언급 요약을 만든다.
// Claude가 교보문고 검색 도구로 ISBN을 고르고, 코드는 검색 결과에 실제로 있었던 ISBN만 받아들인다.
// 언급에 들어 있던 링크는 서점 구매 링크만 빼고 코드가 모두 붙인다.
package extract

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/yonsei-alumni/abc-library/internal/book"
	"github.com/yonsei-alumni/abc-library/internal/kyobo"
)

const (
	model         = "claude-opus-5-5"
	maxTurns      = 8
	maxCandidates = 8
	maxTags       = 5
)

// ErrRefused는 Claude가 게시글 처리를 거절했다는 뜻이다.
var ErrRefused = errors.New("claude가 게시글 처리를 거절했습니다")

// Image는 게시글에 첨부된 사진이다.
type Image struct {
	MediaType string
	Data      []byte
}

// Comment는 게시글의 댓글 하나다. 작성자는 담지 않는다.
type Comment struct {
	At    time.Time
	Text  string
	Links []book.Link
}

// Input은 추출할 게시글의 내용이다.
type Input struct {
	PostedAt time.Time
	Text     string
	// Links는 본문의 링크다.
	Links    []book.Link
	Images   []Image
	Comments []Comment
}

// Searcher는 교보문고 검색이다.
type Searcher interface {
	Search(ctx context.Context, query string) ([]kyobo.Edition, error)
}

// MessageAPI는 Claude Messages API다. *anthropic.BetaMessageService가 구현한다.
type MessageAPI interface {
	New(ctx context.Context, params anthropic.BetaMessageNewParams, opts ...option.RequestOption) (*anthropic.BetaMessage, error)
}

// Extractor는 게시글 하나에서 책과 언급 요약을 뽑는다.
type Extractor struct {
	api    MessageAPI
	search Searcher
}

// New는 Extractor를 만든다.
func New(api MessageAPI, search Searcher) *Extractor {
	return &Extractor{api: api, search: search}
}

type searchInput struct {
	Query string `json:"query"`
}

type result struct {
	Books []struct {
		Title   string   `json:"title"`
		Authors []string `json:"authors"`
		ISBN13  string   `json:"isbn13"`
		Tags    []string `json:"tags"`
		Post    struct {
			Summary string `json:"summary"`
			Links   []int  `json:"links"`
		} `json:"post"`
		Comments []struct {
			Comment int    `json:"comment"`
			Summary string `json:"summary"`
		} `json:"comments"`
	} `json:"books"`
}

// Extract는 게시글(본문과 댓글)에서 찾은 책과, 그 책에 대한 언급 요약을 돌려준다.
func (e *Extractor) Extract(ctx context.Context, in Input) ([]book.Finding, error) {
	seen := map[string]kyobo.Edition{}
	messages := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(userContent(in)...)}
	for range maxTurns {
		resp, err := e.api.New(ctx, params(messages))
		if err != nil {
			return nil, err
		}
		switch resp.StopReason {
		case anthropic.BetaStopReasonRefusal:
			return nil, ErrRefused
		case anthropic.BetaStopReasonToolUse:
			messages = append(messages, resp.ToParam())
			var results []anthropic.BetaContentBlockParamUnion
			for _, block := range resp.Content {
				if use, ok := block.AsAny().(anthropic.BetaToolUseBlock); ok {
					results = append(results, e.runSearch(ctx, use, seen))
				}
			}
			messages = append(messages, anthropic.NewBetaUserMessage(results...))
		case anthropic.BetaStopReasonEndTurn:
			return parseResult(resp, in, seen)
		default:
			return nil, fmt.Errorf("예상하지 못한 stop_reason: %s", resp.StopReason)
		}
	}
	return nil, fmt.Errorf("도구 호출이 %d번을 넘었습니다", maxTurns)
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

func userContent(in Input) []anthropic.BetaContentBlockParamUnion {
	var blocks []anthropic.BetaContentBlockParamUnion
	for _, img := range in.Images {
		blocks = append(blocks, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
			MediaType: anthropic.BetaBase64ImageSourceMediaType(img.MediaType),
			Data:      base64.StdEncoding.EncodeToString(img.Data),
		}))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "게시일: %s\n\n## 본문\n%s\n\n## 본문 링크\n", in.PostedAt.Format("2006-01-02"), strings.TrimSpace(in.Text))
	if len(in.Links) == 0 {
		b.WriteString("(없음)\n")
	}
	for i, l := range in.Links {
		fmt.Fprintf(&b, "[%d] %s %s\n", i+1, l.Title, l.URL)
	}
	b.WriteString("\n## 댓글\n")
	if len(in.Comments) == 0 {
		b.WriteString("(없음)\n")
	}
	for i, c := range in.Comments {
		fmt.Fprintf(&b, "[%d] (%s) %s\n", i+1, c.At.Format("2006-01-02"), strings.TrimSpace(c.Text))
		for _, l := range c.Links {
			fmt.Fprintf(&b, "    링크: %s %s\n", l.Title, l.URL)
		}
	}
	fmt.Fprintf(&b, "\n## 사진\n첨부 사진 %d장을 함께 보냈다.\n", len(in.Images))
	return append(blocks, anthropic.NewBetaTextBlock(b.String()))
}

func (e *Extractor) runSearch(ctx context.Context, use anthropic.BetaToolUseBlock, seen map[string]kyobo.Edition) anthropic.BetaContentBlockParamUnion {
	var in searchInput
	if use.Name != "search_kyobo" || json.Unmarshal([]byte(use.JSON.Input.Raw()), &in) != nil || strings.TrimSpace(in.Query) == "" {
		return anthropic.NewBetaToolResultBlock(use.ID, "알 수 없는 도구 호출입니다. search_kyobo에 query를 넣어 호출하세요.", true)
	}
	eds, err := e.search.Search(ctx, in.Query)
	if err != nil {
		return anthropic.NewBetaToolResultBlock(use.ID, "검색에 실패했습니다: "+err.Error(), true)
	}
	if len(eds) > maxCandidates {
		eds = eds[:maxCandidates]
	}
	for _, ed := range eds {
		seen[ed.ISBN13] = ed
	}
	out, _ := json.Marshal(eds)
	return anthropic.NewBetaToolResultBlock(use.ID, string(out), false)
}

func parseResult(resp *anthropic.BetaMessage, in Input, seen map[string]kyobo.Edition) ([]book.Finding, error) {
	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	var r result
	if err := json.Unmarshal([]byte(text.String()), &r); err != nil {
		return nil, fmt.Errorf("추출 결과를 읽지 못했습니다: %w", err)
	}

	// 같은 작품이 두 번 나오면 처음 것만 쓴다.
	type candidate struct {
		book book.Book
		raw  int
	}
	var candidates []candidate
	keys := map[string]bool{}
	postBooks := 0
	for i, rb := range r.Books {
		b := book.Book{Title: strings.TrimSpace(rb.Title), Authors: trimAll(rb.Authors), Tags: tags(rb.Tags)}
		// 검색 결과에 없던 ISBN은 Claude가 지어냈을 수 있으므로 버린다.
		if ed, ok := seen[rb.ISBN13]; ok {
			b.ISBN13, b.Title, b.Publisher, b.KyoboPID = ed.ISBN13, ed.Title, ed.Publisher, ed.PID
			if len(ed.Authors) > 0 {
				b.Authors = ed.Authors
			}
		}
		key := book.WorkKey(b.Title, b.Authors)
		if b.Title == "" || keys[key] {
			continue
		}
		keys[key] = true
		candidates = append(candidates, candidate{book: b, raw: i})
		if strings.TrimSpace(rb.Post.Summary) != "" {
			postBooks++
		}
	}

	var findings []book.Finding
	for _, c := range candidates {
		b, rb := c.book, r.Books[c.raw]
		var mentions []book.Mention
		if summary := strings.TrimSpace(rb.Post.Summary); summary != "" {
			ids := rb.Post.Links
			// 본문이 책 한 권만 다뤘으면 본문 링크는 모두 그 책의 언급이다.
			if postBooks == 1 {
				ids = nil
				for i := range in.Links {
					ids = append(ids, i+1)
				}
			}
			mentions = append(mentions, book.Mention{Kind: book.InPost, At: in.PostedAt, Summary: summary, Links: pickLinks(in.Links, ids)})
		}
		used := map[int]bool{}
		for _, rc := range rb.Comments {
			summary := strings.TrimSpace(rc.Summary)
			if rc.Comment < 1 || rc.Comment > len(in.Comments) || used[rc.Comment] || summary == "" {
				continue
			}
			used[rc.Comment] = true
			c := in.Comments[rc.Comment-1]
			mentions = append(mentions, book.Mention{Kind: book.InComment, At: c.At, Summary: summary, Links: pickLinks(c.Links, all(len(c.Links)))})
		}
		if len(mentions) == 0 {
			continue
		}
		findings = append(findings, book.Finding{Book: b, Mentions: mentions})
	}
	return findings, nil
}

func all(n int) []int {
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i + 1
	}
	return ids
}

// pickLinks는 1부터 시작하는 번호로 링크를 고른다. 서점 구매 링크와 잘못된 번호는 뺀다.
func pickLinks(links []book.Link, ids []int) []book.Link {
	var out []book.Link
	seen := map[string]bool{}
	for _, id := range ids {
		if id < 1 || id > len(links) {
			continue
		}
		l := links[id-1]
		u, err := url.Parse(l.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || seen[l.URL] || IsStoreLink(u) {
			continue
		}
		seen[l.URL] = true
		title := strings.TrimSpace(l.Title)
		if title == "" {
			title = u.Host
		}
		out = append(out, book.Link{URL: l.URL, Title: title})
	}
	return out
}

var storeHosts = []string{
	"kyobobook.co.kr", "yes24.com", "aladin.co.kr", "interpark.com", "ypbooks.co.kr",
	"bandinlunis.com", "coupang.com", "ridibooks.com", "11st.co.kr", "gmarket.co.kr",
}

// IsStoreLink는 서점이나 쇼핑몰의 상품 링크인지 알려 준다. 구매 링크는 교보문고로 통일하므로 언급에서 뺀다.
func IsStoreLink(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	for _, h := range storeHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return strings.HasPrefix(host, "amazon.") || strings.Contains(host, ".amazon.") ||
		(host == "smartstore.naver.com" || host == "shopping.naver.com" || host == "search.shopping.naver.com")
}

func tags(ts []string) []string {
	var out []string
	for _, t := range ts {
		if slices.Contains(book.Tags, t) && !slices.Contains(out, t) && len(out) < maxTags {
			out = append(out, t)
		}
	}
	return out
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
