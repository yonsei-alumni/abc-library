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
