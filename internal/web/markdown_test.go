package web

import (
	"strings"
	"testing"
)

func TestMarkdown(t *testing.T) {
	got := string(markdown("# 소개\n첫 줄\n둘째 줄\n\n- 목록\n\n~~취소~~ https://example.com/b"))
	for _, want := range []string{
		"<h1>소개</h1>", "첫 줄<br>\n둘째 줄", "<li>목록</li>", "<del>취소</del>",
		`<a href="https://example.com/b" target="_blank" rel="noopener nofollow ugc">https://example.com/b</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown에 %q가 없다:\n%s", want, got)
		}
	}
}

func TestMarkdownDropsUnsafeContent(t *testing.T) {
	for _, src := range []string{
		"<script>alert(1)</script>",
		`<img src=x onerror="alert(1)">`,
		"<iframe src=\"https://evil.example.com\"></iframe>",
		"[링크](javascript:alert(1))",
		"[링크](JavaScript:alert(1))",
		"[링크](jav&#x61;script:alert(1))",
		"[링크](data:text/html;base64,PHNjcmlwdD4=)",
		"![사진](javascript:alert(1))",
		`[링크](https://example.com/" onclick="alert(1))`,
		"<javascript:alert(1)>",
	} {
		got := strings.ToLower(string(markdown(src)))
		// 위험한 주소는 글자로는 남아도 href나 src에 들어가면 안 된다.
		for _, bad := range []string{"<script", "<img src=x", "<iframe", `href="javascript`, `src="javascript`, `href="data:`, "onerror=", `" onclick`} {
			if strings.Contains(got, bad) {
				t.Errorf("markdown(%q) = %q, %q가 남았다", src, got, bad)
			}
		}
	}
}

func TestMarkdownText(t *testing.T) {
	for in, want := range map[string]string{
		"": "",
		"# 제목\n첫 줄\n둘째 줄\n\n- 목록":                                  "제목 첫 줄 둘째 줄 목록",
		"**굵게** [링크](https://example.com/a) https://example.com/b": "굵게 링크 https://example.com/b",
		"<script>alert(1)</script>\n\n`코드` 뒤\n\n```\nfenced\n```":  "뒤",
	} {
		if got := markdownText(in); got != want {
			t.Errorf("markdownText(%q) = %q, want %q", in, got, want)
		}
	}
}
