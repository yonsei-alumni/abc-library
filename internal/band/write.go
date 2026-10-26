package band

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// 밴드에 쓰는 동작은 이 파일에만 둔다. 대화방 주간 요약 글을 일주일에 한 번 올리는 것 말고는 쓰지 않는다.
// 글쓰기 설정(알림, 공지 등록 등)은 건드리지 않고 기본값으로 올린다.

const (
	writeLayer  = "section._lyWrap"
	writeEditor = writeLayer + " div._richEditor[contenteditable=true]"
	// 편집기는 글 속 주소마다 링크 미리보기 카드를 붙인다. 요약에는 주소가 수십 개라 카드를 지운다(본문의 링크는 남는다).
	writeSnippetDelete = writeEditor + ` .cke_widget_wrapper[data-type="snippet"] button._btnDelete`
	writeSubmit        = writeLayer + " button._btnSubmitPost"
	openWriteBtn       = "button._btnOpenWriteLayer"
)

// recentTextsScript는 게시글 목록 첫 화면에 그려진 글의 본문만 읽는다. 작성자 영역은 읽지 않는다.
const recentTextsScript = `() => JSON.stringify([...document.querySelectorAll('article._postMainWrap')]
  .map(card => { const t = card.querySelector('.postBody .txtBody'); return t ? t.innerText : ''; })
  .filter(t => t))`

// RecentTexts는 게시글 목록 첫 화면에 나온 최근 글들의 본문을 읽는다. 같은 글을 두 번 올리지 않으려고 확인할 때 쓴다.
func (c *Crawler) RecentTexts() ([]string, error) {
	if err := c.open(fmt.Sprintf("https://band.us/band/%s/post", c.BandNo)); err != nil {
		return nil, err
	}
	if err := c.waitFor(c.page.Locator("article._postMainWrap").First(), 20000); err != nil {
		return nil, fmt.Errorf("게시글 목록을 읽지 못했습니다: %w", err)
	}
	v, err := c.page.Evaluate(recentTextsScript)
	if err != nil {
		return nil, err
	}
	raw, _ := v.(string)
	var texts []string
	if err := json.Unmarshal([]byte(raw), &texts); err != nil {
		return nil, fmt.Errorf("게시글 목록을 읽지 못했습니다: %w", err)
	}
	return texts, nil
}

// Posted는 최근 글 중에 marker가 든 글이 있는지 알려 준다.
func (c *Crawler) Posted(marker string) (bool, error) {
	texts, err := c.RecentTexts()
	if err != nil {
		return false, err
	}
	return ContainsMarker(texts, marker), nil
}

// ContainsMarker는 texts 중에 marker가 든 글이 있는지 알려 준다. 밴드는 줄마다 문단을 만들어 빈 줄을 늘리므로 공백을 무시하고 비교한다.
func ContainsMarker(texts []string, marker string) bool {
	m := strings.Join(strings.Fields(marker), " ")
	if m == "" {
		return false
	}
	for _, t := range texts {
		if strings.Contains(strings.Join(strings.Fields(t), " "), m) {
			return true
		}
	}
	return false
}

// Write는 content를 밴드에 새 글로 올린다. 글쓰기 창에 넣은 내용이 content와 같은지 확인한 뒤 게시한다.
// 올린 뒤 게시글 목록에서 marker가 든 글을 찾아 올라갔는지 확인한다.
// 오류 메시지에는 글 내용을 넣지 않는다.
func (c *Crawler) Write(content, marker string) error {
	got, err := c.fillEditor(content)
	if err != nil {
		return err
	}
	if !SameLines(got, content) {
		return fmt.Errorf("글쓰기 편집기에 넣은 내용이 요약과 달라서 올리지 않았습니다(%s)", lineDiff(got, content))
	}
	click := playwright.LocatorClickOptions{Timeout: playwright.Float(10000)}
	if err := c.page.Locator(writeSubmit).First().Click(click); err != nil {
		return fmt.Errorf("게시 버튼을 누르지 못했습니다: %w", err)
	}
	editor := c.page.Locator(writeEditor).First()
	if err := editor.WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateHidden, Timeout: playwright.Float(30000)}); err != nil {
		return fmt.Errorf("게시한 뒤 글쓰기 창이 닫히지 않았습니다: %w", err)
	}
	time.Sleep(3 * time.Second)
	posted, err := c.Posted(marker)
	if err != nil {
		return fmt.Errorf("게시했지만 목록에서 확인하지 못했습니다: %w", err)
	}
	if !posted {
		return errors.New("게시했지만 목록에서 새 글을 찾지 못했습니다")
	}
	return nil
}

// fillEditor는 글쓰기 창을 열어 content를 넣고, 편집기에 들어간 글을 줄 단위로 읽어 돌려준다. 게시하지는 않는다.
func (c *Crawler) fillEditor(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", errors.New("올릴 글이 비어 있습니다")
	}
	if err := c.open(fmt.Sprintf("https://band.us/band/%s/post", c.BandNo)); err != nil {
		return "", err
	}
	click := playwright.LocatorClickOptions{Timeout: playwright.Float(10000)}
	opener := c.page.Locator(openWriteBtn).First()
	if err := c.waitFor(opener, 20000); err != nil {
		return "", fmt.Errorf("글쓰기 버튼을 찾지 못했습니다: %w", err)
	}
	if err := opener.Click(click); err != nil {
		return "", fmt.Errorf("글쓰기 창을 열지 못했습니다: %w", err)
	}
	editor := c.page.Locator(writeEditor).First()
	if err := editor.WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible, Timeout: playwright.Float(15000)}); err != nil {
		return "", fmt.Errorf("글쓰기 편집기를 찾지 못했습니다: %w", err)
	}
	if err := editor.Click(click); err != nil {
		return "", err
	}
	// 줄마다 입력하면 편집기가 주소의 미리보기 카드를 만드는 동안 입력을 잃는다(2026-10 실측: 231줄 중 수십 줄).
	// 붙여넣기 이벤트로 한 번에 넣으면 줄마다 문단이 되고 빠지는 줄이 없다.
	if _, err := editor.Evaluate(`(e, text) => { const dt = new DataTransfer(); dt.setData('text/plain', text);
	  e.dispatchEvent(new ClipboardEvent('paste', {clipboardData: dt, bubbles: true, cancelable: true})); }`, strings.TrimSpace(content)); err != nil {
		return "", fmt.Errorf("글쓰기 편집기에 글을 넣지 못했습니다: %w", err)
	}
	if err := c.removeSnippets(); err != nil {
		return "", err
	}
	typed, err := editor.Evaluate(`e => [...e.querySelectorAll(':scope > p')].map(p => p.innerText.replace(/\n$/, '')).join('\n')`, nil)
	if err != nil {
		return "", err
	}
	got, _ := typed.(string)
	return got, nil
}

// removeSnippets는 편집기가 붙인 링크 미리보기 카드를 모두 지운다. 카드는 주소마다 조금씩 늦게 붙으므로
// 지운 뒤 잠시 기다려 새 카드가 없을 때까지 되풀이한다.
func (c *Crawler) removeSnippets() error {
	// 카드는 편집기 밖으로 스크롤돼 있어 Playwright의 클릭이 닿지 않으므로 페이지에서 버튼을 직접 누른다.
	const script = `sel => { const bs = [...document.querySelectorAll(sel)]; bs.forEach(b => b.click()); return bs.length; }`
	for round := 0; round < 5; round++ {
		time.Sleep(3 * time.Second)
		v, err := c.page.Evaluate(script, writeSnippetDelete)
		if err != nil {
			return fmt.Errorf("링크 미리보기를 지우지 못했습니다: %w", err)
		}
		if n, _ := v.(int); n == 0 {
			return nil
		}
	}
	return errors.New("링크 미리보기가 계속 생겨서 지우지 못했습니다")
}

// lineDiff는 처음 달라지는 줄 번호와 양쪽 줄 수를 알려 준다. 글 내용은 넣지 않는다.
func lineDiff(got, content string) string {
	g := strings.Split(strings.TrimSpace(got), "\n")
	w := strings.Split(strings.TrimSpace(content), "\n")
	n := 0
	for n < len(g) && n < len(w) && strings.TrimRight(g[n], " \t") == strings.TrimRight(w[n], " \t") {
		n++
	}
	return fmt.Sprintf("%d번째 줄부터 다름, 편집기 %d줄, 요약 %d줄", n+1, len(g), len(w))
}

// SameLines는 편집기에서 읽은 글이 content와 같은지 줄 단위로 비교한다. 줄 끝 공백은 무시한다.
func SameLines(got, content string) bool {
	norm := func(s string) string {
		lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, " ", " ")), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t")
		}
		return strings.Join(lines, "\n")
	}
	return norm(got) == norm(content)
}
