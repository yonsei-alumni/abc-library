package main

import (
	"testing"
	"time"
)

func TestRequireLocalDB(t *testing.T) {
	for _, dsn := range []string{
		"postgres://abc@localhost:5433/abc_library?sslmode=disable",
		"postgres://abc@127.0.0.1/abc_library",
		"postgres://abc@[::1]:5433/abc_library",
	} {
		t.Setenv("DB_URL", dsn)
		if err := requireLocalDB(); err != nil {
			t.Errorf("%s: %v", dsn, err)
		}
	}
	for _, dsn := range []string{
		"", "postgres://abc@ep-example.ap-southeast-1.aws.neon.tech/abc_library?sslmode=require",
		"postgres://abc@localhost.example.com/abc_library", "postgres://abc@10.0.0.5/abc_library", "://",
	} {
		t.Setenv("DB_URL", dsn)
		if err := requireLocalDB(); err == nil {
			t.Errorf("%q: 로컬 DB가 아니므로 거절해야 한다", dsn)
		}
	}
}

func TestDemoPosts(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	posts := demoPosts(now)
	seen := map[int]bool{}
	for _, p := range posts {
		if seen[p.post.No] {
			t.Errorf("글 번호 %d가 겹친다", p.post.No)
		}
		seen[p.post.No] = true
		for _, f := range p.findings {
			if len(f.Mentions) == 0 {
				t.Errorf("글 %d의 %s에 언급이 없으면 저장되지 않는다", p.post.No, f.Book.Title)
			}
			for _, m := range f.Mentions {
				if m.At.Before(p.post.PostedAt) || m.At.After(now) {
					t.Errorf("글 %d의 언급 시각 %v가 게시 시각 %v와 지금 사이에 있어야 한다", p.post.No, m.At, p.post.PostedAt)
				}
			}
		}
	}
}
