package book

import (
	"strings"
	"testing"
)

func TestCleanCategory(t *testing.T) {
	c, err := CleanCategory(Category{Title: "  올해 읽을 책 ", PublicTitle: " 추천 SF ", Body: "첫 줄\r\n\r\n- 둘째\r\n  "})
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "올해 읽을 책" || c.PublicTitle != "추천 SF" || c.Body != "첫 줄\n\n- 둘째" {
		t.Errorf("정리한 값 = %+v", c)
	}

	long := strings.Repeat("가", MaxCategoryTitle)
	if _, err := CleanCategory(Category{Title: long, PublicTitle: long, Body: strings.Repeat("나", MaxCategoryBody)}); err != nil {
		t.Errorf("최대 길이는 받아야 한다: %v", err)
	}
	for name, c := range map[string]Category{
		"빈 제목":       {Title: "  \n "},
		"긴 제목":       {Title: long + "가"},
		"긴 공개용 제목":   {Title: "책", PublicTitle: long + "가"},
		"긴 본문":       {Title: "책", Body: strings.Repeat("나", MaxCategoryBody+1)},
		"공백만 있는 공개용": {Title: "", PublicTitle: "공개"},
	} {
		if _, err := CleanCategory(c); err == nil {
			t.Errorf("%s: 오류여야 한다", name)
		}
	}
}

func TestShownTitle(t *testing.T) {
	if got := (Category{Title: "나만 보는 제목"}).ShownTitle(); got != "나만 보는 제목" {
		t.Errorf("공개용 제목이 없으면 제목 = %q", got)
	}
	if got := (Category{Title: "나만 보는 제목", PublicTitle: "함께 읽을 책"}).ShownTitle(); got != "함께 읽을 책" {
		t.Errorf("공개용 제목 = %q", got)
	}
}

func TestNewCode(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		code := NewCode(CodeLen)
		if len(code) != CodeLen || !ValidCode(code) {
			t.Fatalf("NewCode() = %q, 영어 소문자 %d자여야 한다", code, CodeLen)
		}
		seen[code] = true
	}
	// 3자는 17,576 가지라서 1000개 중 몇십 개는 겹친다. 겹치면 저장할 때 다시 뽑는다.
	if len(seen) < 940 {
		t.Errorf("1000개 중 %d개만 달랐다. 고르게 뽑지 않는다", len(seen))
	}
	if code := NewCode(4); len(code) != 4 || !ValidCode(code) {
		t.Errorf("NewCode(4) = %q", code)
	}
	for _, good := range []string{"abc", "xyzw", "abcdefgh"} {
		if !ValidCode(good) {
			t.Errorf("ValidCode(%q)는 참이어야 한다", good)
		}
	}
	for _, bad := range []string{"", "ab", "abcdefghi", "Abc", "ab1", "ab-", "abé", "../", "a c"} {
		if ValidCode(bad) {
			t.Errorf("ValidCode(%q)는 거짓이어야 한다", bad)
		}
	}
}
