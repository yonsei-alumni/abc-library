package config

import "testing"

func TestDatabaseURL(t *testing.T) {
	t.Setenv("DB_URL", "postgres://owner@db.example.com/abc?sslmode=require")
	t.Setenv("DB_PWD", "p@ss/word")

	got, err := DatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://owner:p%40ss%2Fword@db.example.com/abc?sslmode=require"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDatabaseURLErrors(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		pwd   string
		valid bool
	}{
		{name: "DB_URL 없음", url: "", pwd: "x"},
		{name: "사용자 이름 없음", url: "postgres://db.example.com/abc", pwd: "x"},
		{name: "DB_URL에 비밀번호 포함", url: "postgres://owner:secret@db.example.com/abc", pwd: "x"},
		{name: "DB_PWD 없음", url: "postgres://owner@db.example.com/abc", pwd: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DB_URL", tt.url)
			t.Setenv("DB_PWD", tt.pwd)
			if _, err := DatabaseURL(); err == nil {
				t.Error("오류가 나야 한다")
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Setenv("ABC_TEST_KEY", "")
	if got := Get("ABC_TEST_KEY", "기본값"); got != "기본값" {
		t.Errorf("빈 값이면 기본값이어야 한다: %q", got)
	}
	t.Setenv("ABC_TEST_KEY", "설정값")
	if got := Get("ABC_TEST_KEY", "기본값"); got != "설정값" {
		t.Errorf("got %q", got)
	}
}
