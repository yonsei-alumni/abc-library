package book

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Category는 사용자가 북마크를 묶는 카테고리다. Body는 Markdown이다.
// Public이 참이면 Code로 만든 짧은 주소에서 누구나 공개용 제목, 본문, 책 목록을 본다.
type Category struct {
	ID int64
	// UserID는 카테고리를 만든 사용자다.
	UserID      int64
	Title       string
	Body        string
	PublicTitle string
	Public      bool
	Code        string
	// Books는 이 카테고리에 넣은 책 수다.
	Books int
}

// ShownTitle은 공개 화면에 보일 제목이다. 공개용 제목이 비었으면 제목을 쓴다.
func (c Category) ShownTitle() string {
	if c.PublicTitle != "" {
		return c.PublicTitle
	}
	return c.Title
}

// Bookmark는 사용자가 북마크한 책과 그 책을 넣은 카테고리다.
type Bookmark struct {
	Book       Book
	Categories []int64
	At         time.Time
}

const (
	// MaxCategoryTitle은 제목과 공개용 제목의 최대 글자 수다.
	MaxCategoryTitle = 100
	// MaxCategoryBody는 본문의 최대 글자 수다.
	MaxCategoryBody = 20000
	// MaxCategories는 사용자 한 명이 만들 수 있는 카테고리 수다.
	MaxCategories = 100
)

// CleanCategory는 입력한 카테고리의 제목 앞뒤 공백을 지우고, 본문의 줄바꿈을 \n으로 맞춘 뒤 길이를 확인한다.
func CleanCategory(c Category) (Category, error) {
	c.Title = strings.TrimSpace(c.Title)
	c.PublicTitle = strings.TrimSpace(c.PublicTitle)
	c.Body = strings.TrimSpace(strings.ReplaceAll(c.Body, "\r\n", "\n"))
	switch {
	case c.Title == "":
		return c, errors.New("제목을 입력해 주세요")
	case utf8.RuneCountInString(c.Title) > MaxCategoryTitle:
		return c, fmt.Errorf("제목은 %d자까지 쓸 수 있습니다", MaxCategoryTitle)
	case utf8.RuneCountInString(c.PublicTitle) > MaxCategoryTitle:
		return c, fmt.Errorf("공개용 제목은 %d자까지 쓸 수 있습니다", MaxCategoryTitle)
	case utf8.RuneCountInString(c.Body) > MaxCategoryBody:
		return c, fmt.Errorf("본문은 %d자까지 쓸 수 있습니다", MaxCategoryBody)
	}
	return c, nil
}

// codeLetters는 공개 주소 코드에 쓰는 글자다. 읽고 받아 적기 쉽게 영어 소문자만 쓴다.
const codeLetters = "abcdefghijklmnopqrstuvwxyz"

// CodeLen은 새로 만드는 공개 주소 코드의 길이다. 26^3(17,576) 가지다.
// 겹치는 코드가 많아지면 저장할 때 한 글자 더 긴 코드를 뽑는다.
const CodeLen = 3

// maxCodeLen은 받아들이는 코드의 최대 길이다.
const maxCodeLen = 8

// NewCode는 공개 주소에 쓰는 임의의 영어 소문자 코드다. n은 길이다.
// 번호를 바꾼 것이 아니라서 만든 순서로 주소를 짐작할 수 없다.
func NewCode(n int) string {
	code := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(code) < n {
		_, _ = rand.Read(buf)
		for _, b := range buf {
			// 26의 배수(234) 밖의 값은 버려서 글자마다 확률이 같게 한다.
			if b < 234 && len(code) < n {
				code = append(code, codeLetters[b%26])
			}
		}
	}
	return string(code)
}

// ValidCode는 s가 공개 주소 코드의 꼴(영어 소문자 CodeLen~8자)인지 알려 준다.
func ValidCode(s string) bool {
	if len(s) < CodeLen || len(s) > maxCodeLen {
		return false
	}
	for i := range len(s) {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}
