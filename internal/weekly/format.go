package weekly

import (
	"fmt"
	"strings"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

// Text는 밴드에 올릴 글이다. 밴드는 들여쓰기를 살리지 않으므로 제목 줄과 하이픈 목록만 쓰고, 이모지는 쓰지 않는다.
// 밴드는 #으로 시작하는 말을 해시태그로 바꾸므로 제목에 #을 쓰지 않는다.
func (s Summary) Text() string {
	var b strings.Builder
	b.WriteString("ABC 대화방 주간 요약\n")
	b.WriteString(s.PeriodLine() + "\n")
	fmt.Fprintf(&b, "기준: 수집된 %s 메시지 %d건\n", s.Room, s.Count)
	if s.Count == 0 {
		b.WriteString("\n이 기간에 수집된 메시지가 없습니다.\n")
		return b.String()
	}
	b.WriteString("참고: 수집된 메시지 기준입니다. 사진은 폰에서 읽은 글자만 근거로 삼았습니다.\n")

	b.WriteString("\n[공유·추천된 책]\n")
	if len(s.Books) == 0 {
		b.WriteString("\n- 이번 주에 공유된 책이 없습니다.\n")
	}
	for _, bk := range s.Books {
		author := strings.Join(bk.Authors, ", ")
		if author == "" {
			author = "저자 미확인"
		}
		fmt.Fprintf(&b, "\n■ 《%s》 - %s\n", plain(bk.Title), plain(author))
		for _, c := range bk.Context {
			fmt.Fprintf(&b, "- %s\n", plain(c))
		}
		for _, l := range bk.Links {
			if l.Title != "" {
				fmt.Fprintf(&b, "- 관련 링크: %s (%s)\n", l.URL, plain(l.Title))
			} else {
				fmt.Fprintf(&b, "- 관련 링크: %s\n", l.URL)
			}
		}
		if bk.Searched {
			fmt.Fprintf(&b, "- 구매 링크(교보문고 검색): %s\n", bk.Purchase)
		} else {
			fmt.Fprintf(&b, "- 구매 링크: %s\n", bk.Purchase)
		}
		if bk.Page != "" {
			fmt.Fprintf(&b, "- ABC 도서관: %s\n", bk.Page)
		}
	}

	b.WriteString("\n[공유된 URL 모음]\n")
	if len(s.Links) == 0 {
		b.WriteString("\n- 책 말고 공유된 URL이 없습니다.\n")
	}
	for _, l := range s.Links {
		title := l.Title
		if title == "" {
			title = "제목 미확인"
		}
		fmt.Fprintf(&b, "\n■ %s\n", plain(title))
		if l.Context != "" {
			fmt.Fprintf(&b, "- 맥락: %s\n", plain(l.Context))
		}
		fmt.Fprintf(&b, "- URL: %s\n", l.URL)
	}

	fmt.Fprintf(&b, "\n[핵심 키워드 %d개]\n", len(s.Keywords))
	for _, k := range s.Keywords {
		fmt.Fprintf(&b, "- %s\n", plain(k))
	}
	return b.String()
}

// plain은 글자 속 #을 전각 ＃으로 바꾼다. 밴드는 #으로 시작하는 말을 해시태그로 만든다. 주소에는 쓰지 않는다.
func plain(s string) string {
	return strings.ReplaceAll(s, "#", "＃")
}

// PeriodLine은 요약의 기간 줄이다. 밴드에 같은 주의 요약을 이미 올렸는지 찾는 표시로도 쓴다.
func (s Summary) PeriodLine() string {
	return fmt.Sprintf("기간: %s ~ %s", dayLabel(s.Start), dayLabel(s.End))
}

// dayLabel은 2026-10-19를 2026-10-19(월)로 쓴다. 날짜가 아니면 그대로 둔다.
func dayLabel(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return fmt.Sprintf("%s(%s)", day, weekdays[t.Weekday()])
}

// Period는 end(YYYY-MM-DD)로 끝나는 7일의 시작과 끝 날짜다. end가 비어 있으면 now(서울) 이전의 가장 가까운 일요일을 끝으로 잡는다.
// 월요일 아침에 실행하면 지난주 월요일부터 어제(일요일)까지다.
func Period(now time.Time, end string) (string, string, error) {
	var last time.Time
	if end == "" {
		y, mo, d := now.In(book.Seoul).Date()
		today := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
		back := int(today.Weekday())
		if back == 0 {
			back = 7
		}
		last = today.AddDate(0, 0, -back)
	} else {
		t, err := time.Parse("2006-01-02", end)
		if err != nil {
			return "", "", fmt.Errorf("날짜는 YYYY-MM-DD로 씁니다: %q", end)
		}
		last = t
	}
	return last.AddDate(0, 0, -6).Format("2006-01-02"), last.Format("2006-01-02"), nil
}
