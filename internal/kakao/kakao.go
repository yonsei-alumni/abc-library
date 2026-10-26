// Package kakao는 카카오톡 대화방 메시지를 Google 스프레드시트에서 읽는다.
// 메시지는 운영자의 폰에서 kakao-to-google-sheet 앱이 대화방 화면을 읽어 시트에 쌓고,
// 시트에 붙은 Apps Script 웹 앱이 토큰을 확인한 뒤 JSON으로 돌려준다.
// 메시지 원문과 작성자는 DB에 저장하지 않고 로그에도 남기지 않는다.
package kakao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/yonsei-alumni/abc-library/internal/book"
)

// Message는 대화방 메시지 하나다.
type Message struct {
	ID   string `json:"id"`
	Room string `json:"room"`
	// Date는 화면의 날짜 구분선으로 정한 보낸 날짜(YYYY-MM-DD)다. 알 수 없으면 비어 있다.
	// DateInferred가 참이면 폰이 읽은 값이 아니라 시트의 정리(Arrange)가 앞뒤 메시지로 추정한 날짜다.
	Date         string `json:"date"`
	DateInferred bool   `json:"date_inferred"`
	// Time은 말풍선 옆 시각 라벨에서 읽은 보낸 시각(HH:MM)이다. 알 수 없으면 비어 있다.
	Time   string `json:"time"`
	Sender string `json:"sender"`
	Text   string `json:"text"`
	// CollectedAt은 폰이 메시지를 처음 읽은 시각이다.
	CollectedAt time.Time `json:"collected_at"`
}

// clockSkew는 보낸 날짜를 모르는 메시지의 시각을 수집 시각 앞으로 놓을 때 봐주는 폰 시계의 오차다.
const clockSkew = 5 * time.Minute

// At은 메시지를 보낸 시각의 추정값이다. 정렬과 기간 판정에 쓴다.
//   - 날짜와 시각을 알면 그 시각
//   - 날짜만 알면 그 날짜에 수집 시각의 시:분을 붙인다. 한 번에 몰아 읽은 메시지는 수집 순서가 대화 순서다.
//   - 시각만 알면 수집 시각보다 늦지 않은 가장 가까운 그 시:분
//   - 둘 다 모르면 수집 시각
func (m Message) At() time.Time {
	collected := m.CollectedAt.In(book.Seoul)
	day, dayErr := time.ParseInLocation("2006-01-02", m.Date, book.Seoul)
	clock, clockErr := time.Parse("15:04", m.Time)
	switch {
	case dayErr == nil && clockErr == nil:
		return day.Add(time.Duration(clock.Hour())*time.Hour + time.Duration(clock.Minute())*time.Minute)
	case dayErr == nil:
		y, mo, d := day.Date()
		return time.Date(y, mo, d, collected.Hour(), collected.Minute(), collected.Second(), 0, book.Seoul)
	case clockErr == nil:
		y, mo, d := collected.Date()
		at := time.Date(y, mo, d, clock.Hour(), clock.Minute(), 0, 0, book.Seoul)
		if at.After(collected.Add(clockSkew)) {
			at = at.AddDate(0, 0, -1)
		}
		return at
	default:
		return collected
	}
}

// Week는 보낸 시각(At)의 날짜가 start~end(YYYY-MM-DD, 양 끝 포함)인 메시지만 보낸 순서로 돌려준다.
// 같은 날 같은 본문은 하나만 남긴다. 폰은 같은 메시지를 한 행으로 합치지만, 앱을 다시 설치하면 다시 올라올 수 있다.
func Week(msgs []Message, start, end string) []Message {
	var out []Message
	for _, m := range msgs {
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		if d := m.At().Format("2006-01-02"); d >= start && d <= end {
			out = append(out, m)
		}
	}
	slices.SortStableFunc(out, func(a, b Message) int { return a.At().Compare(b.At()) })
	seen := map[string]bool{}
	return slices.DeleteFunc(out, func(m Message) bool {
		key := m.At().Format("2006-01-02") + "\x00" + strings.TrimSpace(m.Text)
		if seen[key] {
			return true
		}
		seen[key] = true
		return false
	})
}

// Client는 시트의 Apps Script 웹 앱에서 메시지를 읽는다.
type Client struct {
	HTTP  *http.Client
	URL   string
	Token string
}

// NewClient는 웹 앱 주소(…/exec)와 토큰으로 Client를 만든다.
func NewClient(endpoint, token string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 60 * time.Second}, URL: endpoint, Token: token}
}

type response struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error"`
	Messages []Message `json:"messages"`
	ArrangeResult
}

// ArrangeResult는 시트 정리의 결과다.
type ArrangeResult struct {
	Rows       int `json:"rows"`
	Inferred   int `json:"inferred"`
	StillBlank int `json:"still_blank"`
}

// Arrange는 시트에서 보낸 날짜가 빈 메시지의 날짜를 앞뒤 메시지로 추정해 채우고, 탭마다 대화 순서로 정렬하게 한다.
// 요약하기 전에 불러서 날짜를 모르는 메시지가 수집한 날로 몰리지 않게 한다.
func (c *Client) Arrange(ctx context.Context) (ArrangeResult, error) {
	if c.URL == "" || c.Token == "" {
		return ArrangeResult{}, errors.New("KAKAO_SHEET_URL과 KAKAO_SHEET_TOKEN 환경 변수가 필요합니다")
	}
	body, _ := json.Marshal(map[string]string{"token": c.Token, "action": "arrange"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return ArrangeResult{}, errors.New("KAKAO_SHEET_URL 형식이 잘못되었습니다")
	}
	req.Header.Set("Content-Type", "application/json")
	// Apps Script는 처리한 뒤 302로 결과 주소를 알려 준다. Go의 클라이언트는 POST의 302를 GET으로 따라간다.
	r, err := c.do(req)
	if err != nil {
		return ArrangeResult{}, err
	}
	return r.ArrangeResult, nil
}

// Messages는 room 대화방에서 보낸 날짜가 start~end(YYYY-MM-DD)인 메시지와, 날짜를 모르지만 그 기간에 수집한 메시지를 읽는다.
// 순서는 정해져 있지 않다. Week로 거르고 정렬한다.
func (c *Client) Messages(ctx context.Context, room, start, end string) ([]Message, error) {
	if c.URL == "" || c.Token == "" {
		return nil, errors.New("KAKAO_SHEET_URL과 KAKAO_SHEET_TOKEN 환경 변수가 필요합니다")
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, errors.New("KAKAO_SHEET_URL 형식이 잘못되었습니다")
	}
	q := u.Query()
	q.Set("token", c.Token)
	q.Set("action", "messages")
	q.Set("room", room)
	q.Set("start", start)
	q.Set("end", end)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	r, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return r.Messages, nil
}

// do는 웹 앱에 요청하고 응답을 읽는다. 웹 앱은 늘 200으로 답하고 성공 여부를 본문의 ok로 알린다.
func (c *Client) do(req *http.Request) (response, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// 오류 메시지에 토큰이 든 주소가 들어가지 않게 한다.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return response{}, fmt.Errorf("시트 요청 실패: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return response{}, fmt.Errorf("시트 응답 읽기 실패: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return response{}, fmt.Errorf("시트 응답 코드 %d", resp.StatusCode)
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		// 토큰이 틀리거나 웹 앱 권한이 없으면 Google 로그인 HTML이 온다.
		return response{}, errors.New("시트 응답이 JSON이 아닙니다. 웹 앱 주소와 배포 권한(모든 사용자)을 확인하세요")
	}
	if !r.OK {
		return response{}, fmt.Errorf("시트 오류: %s", r.Error)
	}
	return r, nil
}
