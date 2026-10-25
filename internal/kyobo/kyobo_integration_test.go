//go:build integration

package kyobo

import (
	"context"
	"slices"
	"testing"
)

// 실제 교보문고에서 상품 번호로 검색하면 그 상품이 나오고, 저자와 출판사를 읽는다.
// 실행: make test-integration
func TestSearchByProductPID(t *testing.T) {
	eds, err := NewClient().Search(context.Background(), "S000213812009")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(eds, func(ed Edition) bool { return ed.PID == "S000213812009" })
	if i < 0 {
		t.Fatalf("상품 번호로 찾은 결과에 그 상품이 없다: %+v", eds)
	}
	if ed := eds[i]; ed.ISBN13 != "9788965966388" || ed.Title != "세계 너머의 세계" || ed.Publisher != "흐름출판" ||
		!slices.Equal(ed.Authors, []string{"에릭 호엘"}) {
		t.Errorf("판본 = %+v", ed)
	}
}
