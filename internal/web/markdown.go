package web

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// md는 카테고리 본문의 Markdown 변환기다. 사용자가 쓴 글이므로 안전한 기본값을 그대로 쓴다.
// 원시 HTML은 빼고, javascript: 같은 위험한 주소의 링크와 이미지는 주소를 비운다(html.WithUnsafe를 쓰지 않는다).
// 한 줄 띄우지 않고 쓴 줄바꿈도 줄바꿈으로 보이게 한다.
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(externalLinks{}, 100))),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// externalLinks는 링크를 새 창에서 열고, 검색엔진이 따라가지 않게 한다.
type externalLinks struct{}

func (externalLinks) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.(type) {
		case *ast.Link, *ast.AutoLink:
			n.SetAttributeString("target", "_blank")
			n.SetAttributeString("rel", "noopener nofollow ugc")
		}
		return ast.WalkContinue, nil
	})
}

// markdown은 Markdown을 HTML로 바꾼다. goldmark가 원시 HTML과 위험한 주소를 걸러 내므로 그대로 싣는다.
func markdown(s string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(s), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(s))
	}
	return template.HTML(buf.String())
}

// markdownText는 Markdown에서 글자만 뽑아 한 줄로 잇는다. 링크는 글자만 남기고, 원시 HTML과 코드는 뺀다.
// 공개 주소의 링크 미리보기 설명에 쓴다.
func markdownText(s string) string {
	src := []byte(s)
	var b strings.Builder
	_ = ast.Walk(md.Parser().Parse(text.NewReader(src)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.HTMLBlock, *ast.RawHTML, *ast.CodeSpan:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			b.Write(n.Segment.Value(src))
			b.WriteByte(' ')
		case *ast.AutoLink:
			b.Write(n.Label(src))
			b.WriteByte(' ')
		case *ast.String:
			b.Write(n.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}
