// 서비스 워커를 등록하고, 주소의 #post-글번호가 가리키는 언급 묶음을 강조한다.
if ("serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js");

// 처음 열 때는 브라우저가 그 묶음으로 스크롤하고, hx-boost로 이동할 때는 htmx가 스크롤한다.
// hx-boost 이동은 history.pushState를 써서 CSS :target이 바뀌지 않으므로 클래스로 강조한다.
function focusThread() {
  for (const el of document.querySelectorAll(".thread-focus")) el.classList.remove("thread-focus");
  const id = decodeURIComponent(location.hash.slice(1));
  const el = id && document.getElementById(id);
  if (el && el.classList.contains("thread")) el.classList.add("thread-focus");
}

document.addEventListener("DOMContentLoaded", focusThread);
document.addEventListener("htmx:afterSettle", focusThread);
window.addEventListener("hashchange", focusThread);

// 내 북마크의 책을 끌어서 순서를 바꾼다. 마우스와 터치를 함께 다루려고 포인터 이벤트를 쓰고,
// 키보드로는 ⠿에서 방향키로 옮긴다. 바꾼 순서는 목록의 data-order-url로 보낸다.
// hx-boost로 화면이 바뀌어도 동작하도록 document에서 이벤트를 받는다.
let drag = null;

function sortStatus(list, text) {
  const status = list.parentElement.querySelector(".sort-status");
  if (status) status.textContent = text;
}

async function saveOrder(list) {
  const body = new URLSearchParams();
  for (const li of list.children) if (li.dataset.id) body.append("book", li.dataset.id);
  try {
    const res = await fetch(list.dataset.orderUrl, { method: "POST", body, credentials: "same-origin" });
    if (!res.ok) throw new Error(res.status);
    sortStatus(list, "순서를 저장했습니다.");
  } catch {
    sortStatus(list, "순서를 저장하지 못했습니다. 화면을 다시 불러옵니다.");
    setTimeout(() => location.reload(), 1500);
  }
}

function orderOf(list) {
  return [...list.children].map((li) => li.dataset.id).join(",");
}

document.addEventListener("pointerdown", (e) => {
  const handle = e.target.closest(".sortable .drag-handle");
  if (!handle || e.button !== 0) return;
  const item = handle.closest("li");
  const list = item.parentElement;
  e.preventDefault();
  handle.setPointerCapture(e.pointerId);
  drag = { item, list, handle, before: orderOf(list) };
  item.classList.add("dragging");
  document.body.classList.add("sorting");
});

document.addEventListener("pointermove", (e) => {
  if (!drag) return;
  // 화면 끝 가까이 끌면 스크롤한다.
  if (e.clientY < 60) window.scrollBy(0, -12);
  else if (e.clientY > window.innerHeight - 60) window.scrollBy(0, 12);
  const over = document.elementFromPoint(e.clientX, e.clientY)?.closest("li");
  if (!over || over === drag.item || over.parentElement !== drag.list) return;
  const items = [...drag.list.children];
  // 뒤에 있는 책 위로 가면 그 뒤에, 앞에 있는 책 위로 가면 그 앞에 둔다.
  if (items.indexOf(over) > items.indexOf(drag.item)) over.after(drag.item);
  else over.before(drag.item);
});

function endDrag() {
  if (!drag) return;
  const { item, list, before } = drag;
  drag = null;
  item.classList.remove("dragging");
  document.body.classList.remove("sorting");
  if (orderOf(list) !== before) saveOrder(list);
}

document.addEventListener("pointerup", endDrag);
document.addEventListener("pointercancel", endDrag);

document.addEventListener("keydown", (e) => {
  const handle = e.target.closest(".sortable .drag-handle");
  if (!handle) return;
  const item = handle.closest("li");
  let moved = false;
  if ((e.key === "ArrowLeft" || e.key === "ArrowUp") && item.previousElementSibling) {
    item.previousElementSibling.before(item);
    moved = true;
  } else if ((e.key === "ArrowRight" || e.key === "ArrowDown") && item.nextElementSibling) {
    item.nextElementSibling.after(item);
    moved = true;
  }
  if (!moved) return;
  e.preventDefault();
  handle.focus();
  saveOrder(item.parentElement);
});
