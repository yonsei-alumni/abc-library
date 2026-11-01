package band

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// 밴드에 쓰는 동작은 이 파일에만 둔다. 대화방 주간 요약 글을 일주일에 한 번 올리는 것 말고는 쓰지 않는다.
// 글쓰기 설정(알림, 공지 등록 등)은 건드리지 않고 기본값으로 올린다. 게시판을 쓰는 밴드에서는 정해진 게시판만 고른다.

const (
	writeLayer  = "section._lyWrap"
	writeEditor = writeLayer + " div._richEditor[contenteditable=true]"
	// 편집기는 글 속 주소마다 링크 미리보기 카드를 붙인다. 요약에는 주소가 수십 개라 카드를 지운다(본문의 링크는 남는다).
	writeSnippetDelete = writeEditor + ` .cke_widget_wrapper[data-type="snippet"] button._btnDelete`
	writeSubmit        = writeLayer + " button._btnSubmitPost"
	openWriteBtn       = "button._btnOpenWriteLayer"

	// boardPrompt는 게시판 선택 창의 안내 문구다. 게시판을 쓰는 밴드는 게시 버튼을 누르면 이 창을 띄우고,
	// 게시판을 고르지 않으면 글을 올리지 않는다.
	boardPrompt = "게시판을 선택해야 글이 등록됩니다"
	// boardClickTimeout은 게시판 선택 창의 항목과 확인을 누를 때 기다리는 시간(밀리초)이다.
	boardClickTimeout = 5000

	// writeCloseTimeout은 게시 버튼을 누른 뒤 글쓰기 창이 닫히기를 기다리는 시간이다.
	writeCloseTimeout = 90 * time.Second
	// maxWriteStates는 게시가 막혔을 때 오류에 남기는 화면 상태의 수다.
	maxWriteStates = 10
)

// writeStateScript는 게시 버튼을 누른 뒤의 화면을 읽는다. 게시가 막힌 이유를 알려고 쓴다.
// 글자는 편집기와 게시글 카드 밖의 안내와 쓰기 창을 덮은 층에서만 읽는다. 편집기는 글자 수만 센다.
const writeStateScript = `({layer, editor, submit}) => {
  const clean = t => (t || '').trim().replace(/\s+/g, ' ').slice(0, 200);
  const shown = e => { const r = e.getBoundingClientRect(), s = getComputedStyle(e);
    return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'; };
  const content = 'article, [contenteditable=true]';
  const ed = document.querySelector(editor);
  const btn = document.querySelector(submit);
  const box = ed && ed.closest(layer);
  const notes = [...document.querySelectorAll(
    '[role=alert], [role=alertdialog], [role=dialog], [class*=toast i], [class*=alert i], [class*=error i], [class*=popup i], [class*=modal i]')]
    .filter(e => shown(e) && !e.contains(ed) && !e.closest(content))
    .map(e => clean(e.innerText)).filter(Boolean);
  // 쓰기 창 위를 덮은 층은 클래스 이름과 상관없이 찾는다. 화면 가운데에 있는 요소가 쓰기 창 밖이면 덮인 것이다.
  const top = document.elementFromPoint(innerWidth / 2, innerHeight / 2);
  let cover = '';
  if (top && box && !box.contains(top) && !top.closest(content)) {
    cover = (top.className || top.tagName).toString().slice(0, 60);
    for (let e = top, i = 0; e && i < 4 && !e.contains(ed) && !e.querySelector('article'); e = e.parentElement, i++) {
      const t = clean(e.innerText);
      if (t) { notes.push(t); break; }
    }
  }
  return JSON.stringify({
    submit: btn ? {disabled: btn.disabled, aria: btn.getAttribute('aria-disabled') || '', class: btn.className.toString().slice(0, 80)} : null,
    chars: ed ? ed.textContent.length : 0,
    paragraphs: ed ? ed.querySelectorAll(':scope > p').length : 0,
    cover,
    notes: [...new Set(notes)].slice(0, 5),
  });
}`

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
	c.takeDialogs()
	click := playwright.LocatorClickOptions{Timeout: playwright.Float(10000)}
	if err := c.page.Locator(writeSubmit).First().Click(click); err != nil {
		return fmt.Errorf("게시 버튼을 누르지 못했습니다: %w", err)
	}
	if err := c.waitWriteClosed(writeCloseTimeout); err != nil {
		return err
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

// waitWriteClosed는 게시 버튼을 누른 뒤 글쓰기 창이 닫히기를 기다린다. 잠깐 떴다 사라지는 안내를 놓치지 않으려고
// 1초마다 화면을 읽고, 끝내 닫히지 않으면 본 상태와 알림창 문구를 오류에 담는다.
// Playwright 오류의 호출 기록에는 요소의 내용이 섞일 수 있으므로 오류에 넣지 않는다.
func (c *Crawler) waitWriteClosed(timeout time.Duration) error {
	editor := c.page.Locator(writeEditor).First()
	var states []string
	chosen := false
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(time.Second) {
		// 게시한 뒤 화면이 바뀌는 중이면 읽기가 실패할 수 있으므로 오류를 실패로 치지 않는다.
		if visible, err := editor.IsVisible(); err == nil && !visible {
			return nil
		}
		s := c.writeState()
		if s != "" && len(states) < maxWriteStates && !slices.Contains(states, s) {
			states = append(states, s)
		}
		// 두 번 올리지 않도록 게시판은 한 번만 고르고, 고른 뒤에 게시 버튼을 다시 누르지 않는다.
		if !chosen {
			var err error
			if chosen, err = c.chooseBoard(); err != nil {
				return err
			}
			if chosen {
				states = append(states, "게시판 "+c.Board+"을 고름")
			}
		}
	}
	return fmt.Errorf("게시한 뒤 글쓰기 창이 닫히지 않았습니다(%s)", describeWrite(c.takeDialogs(), states))
}

// chooseBoard는 게시판 선택 창이 떠 있으면 c.Board를 고르고 확인을 누른다. 창이 없으면 false를 돌려준다.
// 게시판을 쓰지 않는 밴드에는 창이 뜨지 않는다. 오류에는 Playwright의 호출 기록 대신 화면 상태를 넣는다.
func (c *Crawler) chooseBoard() (bool, error) {
	if visible, err := c.page.GetByText(boardPrompt).First().IsVisible(); err != nil || !visible {
		return false, nil
	}
	if c.Board == "" {
		return false, errors.New("게시판 선택 창이 떴지만 고를 게시판이 정해지지 않았습니다")
	}
	exact := playwright.PageGetByTextOptions{Exact: playwright.Bool(true)}
	click := playwright.LocatorClickOptions{Timeout: playwright.Float(boardClickTimeout)}
	// 화면의 게시판 이름은 #으로 시작한다.
	if err := c.page.GetByText("#"+c.Board, exact).Locator("visible=true").First().Click(click); err != nil {
		return false, fmt.Errorf("게시판 %s를 고르지 못했습니다(%s)", c.Board, describeWrite(c.takeDialogs(), []string{c.writeState()}))
	}
	if err := c.page.GetByText("확인", exact).Locator("visible=true").Last().Click(click); err != nil {
		return false, fmt.Errorf("게시판 선택 창의 확인을 누르지 못했습니다(%s)", describeWrite(c.takeDialogs(), []string{c.writeState()}))
	}
	return true, nil
}

// writeState는 지금 화면의 상태를 JSON으로 읽는다. 읽지 못하면 빈 문자열이다.
func (c *Crawler) writeState() string {
	v, err := c.page.Evaluate(writeStateScript, map[string]any{"layer": writeLayer, "editor": writeEditor, "submit": writeSubmit})
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// describeWrite는 글쓰기 창이 닫히지 않았을 때 오류에 넣을 설명을 만든다.
func describeWrite(dialogs, states []string) string {
	var parts []string
	if len(dialogs) > 0 {
		parts = append(parts, "알림창 "+strings.Join(dialogs, " / "))
	}
	if len(states) > 0 {
		parts = append(parts, "화면 "+strings.Join(states, " / "))
	}
	if len(parts) == 0 {
		return "화면 상태를 읽지 못함"
	}
	return strings.Join(parts, ", ")
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
