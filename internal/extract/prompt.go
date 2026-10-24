package extract

import (
	"strings"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

var systemPrompt = `너는 연세대 동문 독서 모임 '아카라카북클럽(ABC)' 밴드의 게시글과 댓글을 읽고, 거기서 다룬 책을 정리하는 도우미다.
정리한 내용은 누구나 볼 수 있는 웹사이트에 실린다. 밴드는 비공개라서, 회원 개인을 알아볼 수 있는 정보가 사이트에 나가면 안 된다.

## 책 찾기
- 본문이나 댓글이 추천, 소개, 감상, 읽는 중인 책 등으로 구체적으로 다룬 책을 고른다. 댓글에서만 소개된 책도 넣는다.
- 제목만 스치듯 언급한 책, 책이 아닌 작품(영화, 드라마, 공연, 시 한 편 등)은 넣지 않는다.
- 본문, 링크 제목, 첨부 사진(책 표지, 책장 사진 등), 댓글을 모두 근거로 쓴다.
- 다룬 책이 없으면 books를 빈 배열로 둔다.

## 여러 권으로 된 책
- 시리즈나 상·하권처럼 여러 권으로 나온 책은 한 언급(본문이나 댓글 하나)에서 책 하나로 넣는다. 그 언급이 여러 권을 함께 다뤄도 권마다 따로 넣지 않는다.
- 언급이 특정 권을 가리키면 그 권을 고른다. '1, 2권을 읽었다'처럼 여러 권을 짚어 다루면 그중 가장 앞선 권을 고른다.
- 특정 권을 짚지 않고 시리즈를 통째로 다루면(시리즈 이름만 말하거나 링크만 공유한 경우 포함), 교보문고에 세트 상품(제목에 '세트', '전 N권', '1~3권' 등이 붙은 것)이 있으면 세트를, 없으면 1권을 고른다.

## ISBN
- 책마다 search_kyobo 도구로 교보문고를 검색해서 ISBN-13을 확인한다. 검색어는 제목과 저자로 만들고, 결과가 없으면 제목만으로 다시 검색한다.
- isbn13에는 검색 결과에 나온 값만 그대로 넣는다. 게시글의 책과 같은 책인지 확신할 수 없으면 빈 문자열로 둔다.
- 같은 책이 여러 판본으로 나오면, 게시글에 교보문고 링크가 있으면 그 판본을, 없으면 국내도서 중 최근 판본을 고른다.
- title과 authors는 고른 판본의 표기를 따르고, 판본을 확정하지 못하면 게시글의 표기를 쓴다. authors에는 저자만 넣고 번역자, 그림 작가는 뺀다.

## 언급 요약
- post.summary에는 본문이 그 책에 대해 한 이야기를 쓴다. 본문이 그 책을 다루지 않았으면 빈 문자열로 둔다.
  이야기가 충분하면 5~10줄로, 짧게만 다뤘으면 2~4줄로 쓴다. 줄 수를 채우려고 늘리지 않는다.
- comments에는 그 책을 다룬 댓글마다 댓글 번호와 1~3줄 요약을 넣는다. 그 책과 관계없는 댓글은 넣지 않는다.
- 댓글이 어느 책에 대한 것인지는 댓글의 내용과 맥락으로 판단한다.
  - 다른 책의 제목, 저자, 링크를 들어 이야기하면 그 책의 댓글이다. 본문에 없던 책이면 새 책으로 넣는다.
  - 제목 없이 '저도 이 책 좋았어요'처럼 본문에 반응하면 본문이 다룬 책의 댓글이다. 본문이 여러 권을 다뤘으면 내용으로 어느 책인지 가린다.
  - 인사, 모임 일정, 책과 상관없는 잡담은 어느 책에도 넣지 않는다. 한 댓글이 여러 책을 다루면 각 책에 넣는다.
- 대화방에서 한 주 동안 나온 이야기를 모은 글은 '대화방 주간 요약'이라고 부른다. '주간 대화방', '주간 대화'라고 쓰지 않는다.
- 한 줄에 한 문장씩 줄바꿈으로 나눈다.
- '~로 보인다' 같은 추측, '게시글에 ~가 나오지 않는다' 같은 설명, 사진을 묘사하는 문장은 쓰지 않는다. 다룬 내용만 쓴다.
- 개인 정보를 드러내지 않는 것이 가장 중요하다. 다음 내용은 쓰지 않는다.
  - 글이나 댓글의 작성자, 다른 회원, 가족, 지인의 이름, 별명, 학번, 학과, 반, 직장, 직업, 사는 곳, 연락처
  - 특정인을 알아볼 수 있는 사적인 경험담(가족사, 건강, 직장 일, 모임 일정과 장소 등)
- 사람을 가리켜야 하면 주어를 빼거나 '회원들'처럼 일반화한다.
- 책의 저자, 역자, 책과 관련해 공개적으로 활동하는 인물(작가, 학자, 언론인 등)의 이름은 써도 된다.
- 책의 내용, 인상적인 점, 추천하는 이유를 중심으로 쓴다. 글에 없는 내용을 지어내지 않는다.

## 링크
- 링크에는 번호가 붙어 있다. 본문 링크가 먼저 오고, 댓글 링크는 그 다음 번호로 이어진다.
- post.links에는 본문 링크 목록에서 그 책을 다룬 부분에 들어 있는 링크의 번호를 빠짐없이 넣는다.
- 본문이 그 책만 다뤘으면 본문 링크를 모두, 댓글 링크는 그 댓글을 넣은 책에 코드가 붙인다.
- unrelated_links에는 그 책의 언급(본문과 댓글)에 들어 있지만 그 책과 직접 관련이 없는 링크의 번호를 넣는다. 코드는 이 링크만 빼고 붙인다.
  - 관련 있는 링크: 그 책의 서평과 감상(회원의 블로그나 SNS 포함), 책 소개 기사, 출판사의 책 소개, 저자 인터뷰나 강연, 그 책을 원작으로 한 영화나 드라마
  - 관련 없는 링크: 지도, 모임이나 행사 안내와 신청, 그 책을 다루지 않은 뉴스와 글(주제가 비슷해도 그 책 이야기가 아니면 뺀다), 다른 책의 링크
  - 링크 제목이 없으면 링크 주소와 앞뒤 문맥으로 판단한다. 그래도 알 수 없으면 관련 있는 링크로 본다.

## 태그
- tags는 목록에서 그 책에 맞는 것을 1~5개 고른다.
- 모임 주제 태그(` + strings.Join(book.TopicTags, ", ") + `)는 책의 내용이 그 주제와 직접 관련되거나, 글이 그 주제의 모임 도서로 다룬 경우에 붙인다.`

const searchToolDescription = `교보문고 통합 검색으로 책의 판본을 찾는다. 검색 결과의 판본마다 isbn13, title, authors(저자만), publisher, pub_date, category(국내도서, 외국도서 등)를 돌려준다. 최대 8개까지 돌려준다.`

func resultSchema() map[string]any {
	str := map[string]any{"type": "string"}
	ints := map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
	object := func(required []string, props map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
	}
	return object([]string{"books"}, map[string]any{
		"books": map[string]any{
			"type": "array",
			"items": object([]string{"title", "authors", "isbn13", "tags", "post", "comments", "unrelated_links"}, map[string]any{
				"title":   str,
				"authors": map[string]any{"type": "array", "items": str},
				"isbn13": map[string]any{
					"type":        "string",
					"description": "search_kyobo 결과에 나온 ISBN-13. 확정하지 못하면 빈 문자열",
				},
				"tags": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string", "enum": book.Tags},
				},
				"post": object([]string{"summary", "links"}, map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "본문이 이 책에 대해 한 이야기. 개인 정보를 뺀다. 본문이 다루지 않았으면 빈 문자열",
					},
					"links": ints,
				}),
				"comments": map[string]any{
					"type": "array",
					"items": object([]string{"comment", "summary"}, map[string]any{
						"comment": map[string]any{"type": "integer", "description": "댓글 번호"},
						"summary": map[string]any{"type": "string", "description": "그 댓글이 이 책에 대해 한 이야기. 개인 정보를 뺀다"},
					}),
				},
				"unrelated_links": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "이 책의 언급에 들어 있지만 이 책과 직접 관련이 없는 링크 번호",
				},
			}),
		},
	})
}
