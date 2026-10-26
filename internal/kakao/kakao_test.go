package kakao

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

func kst(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, book.Seoul)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Message
		want string
	}{
		{"날짜와 시각", Message{Date: "2026-10-20", Time: "21:03", CollectedAt: kst("2026-10-25 10:00")}, "2026-10-20 21:03"},
		{"날짜만: 수집 시각의 시:분", Message{Date: "2026-10-20", CollectedAt: kst("2026-10-25 10:07")}, "2026-10-20 10:07"},
		{"시각만: 수집 시각 이전", Message{Time: "09:30", CollectedAt: kst("2026-10-20 10:00")}, "2026-10-20 09:30"},
		{"시각만: 오차 안의 미래", Message{Time: "10:04", CollectedAt: kst("2026-10-20 10:00")}, "2026-10-20 10:04"},
		{"시각만: 수집 시각보다 늦으면 전날", Message{Time: "23:50", CollectedAt: kst("2026-10-20 00:10")}, "2026-10-19 23:50"},
		{"둘 다 모름", Message{CollectedAt: kst("2026-10-20 08:00")}, "2026-10-20 08:00"},
		{"잘못된 날짜는 모르는 것으로", Message{Date: "2026-13-40", CollectedAt: kst("2026-10-20 08:00")}, "2026-10-20 08:00"},
		{"UTC로 받은 수집 시각", Message{CollectedAt: time.Date(2026, 10, 19, 23, 30, 0, 0, time.UTC)}, "2026-10-20 08:30"},
	} {
		if got := tc.m.At().In(book.Seoul).Format("2006-01-02 15:04"); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestWeekFiltersSortsAndDedupes(t *testing.T) {
	msgs := []Message{
		{ID: "c", Date: "2026-10-21", Time: "09:00", Text: "셋째"},
		{ID: "a", Date: "2026-10-19", Time: "00:00", Text: "첫째"},
		{ID: "x", Date: "2026-10-18", Time: "23:59", Text: "기간 전"},
		{ID: "y", Date: "2026-10-26", Time: "00:00", Text: "기간 후"},
		{ID: "b", Date: "2026-10-20", Time: "12:00", Text: "둘째"},
		{ID: "b2", Date: "2026-10-20", Time: "12:05", Text: " 둘째 "},
		{ID: "d", Date: "2026-10-22", Time: "09:00", Text: "둘째"},
		{ID: "e", Date: "2026-10-25", Time: "23:59", Text: "마지막"},
		{ID: "f", CollectedAt: kst("2026-10-23 07:00"), Text: "날짜 모름"},
		{ID: "g", Date: "2026-10-23", Time: "08:00", Text: "  "},
	}
	got := Week(msgs, "2026-10-19", "2026-10-25")
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if want := "a,b,c,d,f,e"; strings.Join(ids, ",") != want {
		t.Errorf("ids = %v, want %s", ids, want)
	}
}

func TestMessages(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/exec" {
			// Apps Script처럼 다른 주소로 넘긴다.
			query = r.URL.RawQuery
			http.Redirect(w, r, "/echo", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"id":"1","room":"ABC","date":"2026-10-20","time":"21:03","sender":"홍길동","text":"《책》 좋아요","collected_at":"2026-10-20T21:05:00+09:00"}]}`))
	}))
	defer srv.Close()

	msgs, err := NewClient(srv.URL+"/exec", "t0k").Messages(context.Background(), "ABC(아카라카북클럽)", "2026-10-19", "2026-10-25")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "《책》 좋아요" || !msgs[0].CollectedAt.Equal(kst("2026-10-20 21:05")) {
		t.Errorf("msgs = %+v", msgs)
	}
	for _, want := range []string{"token=t0k", "action=messages", "start=2026-10-19", "end=2026-10-25", "room=ABC%28"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q에 %q가 없다", query, want)
		}
	}
}

func TestMessagesErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"시트 오류", `{"ok":false,"error":"토큰이 틀렸습니다"}`, 200, "토큰이 틀렸습니다"},
		{"로그인 HTML", `<html>Sign in</html>`, 200, "JSON이 아닙니다"},
		{"응답 코드", `{}`, 500, "500"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := NewClient(srv.URL, "secret-token").Messages(context.Background(), "ABC", "2026-10-19", "2026-10-25")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}

	if _, err := NewClient("", "").Messages(context.Background(), "ABC", "2026-10-19", "2026-10-25"); err == nil {
		t.Error("설정이 없는데 오류가 없다")
	}

	// 연결 오류에도 토큰이 든 주소를 드러내지 않는다.
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	_, err := NewClient(addr+"/exec", "secret-token").Messages(context.Background(), "ABC", "2026-10-19", "2026-10-25")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Errorf("err = %v", err)
	}
}
