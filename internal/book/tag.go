package book

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TagKind는 태그의 갈래다.
type TagKind string

const (
	// TopicTag는 매달 모임에서 정한 주제다. 최근 주제가 먼저 온다.
	TopicTag TagKind = "topic"
	// KindTag는 소설, 인문, 역사 같은 책의 갈래다.
	KindTag TagKind = "kind"
)

// Tag는 책에 붙일 수 있는 태그다. 태그 목록은 DB의 tags 테이블에 있고 관리 화면에서 고친다.
type Tag struct {
	Name string
	Kind TagKind
}

// TagList는 정해진 순서(모임 주제가 먼저, 그 안에서는 정한 순서)로 늘어놓은 태그 목록이다.
type TagList []Tag

// Names는 모든 태그 이름이다. 추출할 때 이 목록에서만 고르게 한다.
func (l TagList) Names() []string {
	names := make([]string, len(l))
	for i, t := range l {
		names[i] = t.Name
	}
	return names
}

// Topics는 모임 주제 태그 이름이다.
func (l TagList) Topics() []string {
	var names []string
	for _, t := range l {
		if t.Kind == TopicTag {
			names = append(names, t.Name)
		}
	}
	return names
}

// Has는 목록에 그 이름의 태그가 있는지 알려 준다.
func (l TagList) Has(name string) bool {
	return slices.ContainsFunc(l, func(t Tag) bool { return t.Name == name })
}

// IsTopic은 그 이름의 태그가 모임 주제인지 알려 준다.
func (l TagList) IsTopic(name string) bool {
	return slices.ContainsFunc(l, func(t Tag) bool { return t.Name == name && t.Kind == TopicTag })
}

// maxTagName은 태그 이름의 최대 글자 수다.
const maxTagName = 20

// CleanTagName은 태그 이름의 앞뒤 공백과 앞에 붙은 #을 떼고 쓸 수 있는 이름인지 검사한다.
func CleanTagName(s string) (string, error) {
	s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "#"))
	switch {
	case s == "":
		return "", errors.New("태그 이름을 입력해 주세요")
	case utf8.RuneCountInString(s) > maxTagName:
		return "", errors.New("태그 이름은 20자까지 쓸 수 있습니다")
	case strings.ContainsFunc(s, func(r rune) bool { return r == '#' || r == ',' || unicode.IsControl(r) }):
		return "", errors.New("태그 이름에는 #, 쉼표, 제어 문자를 쓸 수 없습니다")
	}
	return s, nil
}
