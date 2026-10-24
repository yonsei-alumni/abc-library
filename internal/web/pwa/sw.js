// ABC 도서관 서비스 워커. 서버가 VERSION과 PRECACHE를 채워서 /sw.js로 내보낸다.
// 정적 파일이 바뀌면 VERSION이 바뀌고, 브라우저가 새 워커를 설치하면서 예전 캐시를 지운다.
const VERSION = '__VERSION__';
const PRECACHE = __PRECACHE__;
const STATIC_CACHE = 'static-' + VERSION;
const PAGE_CACHE = 'pages-' + VERSION;
const MAX_PAGES = 50;
// 관리 화면, 내 북마크, 로그인 흐름은 서비스 워커가 건드리지 않는다.
const BYPASS = ['/admin', '/me', '/login', '/logout', '/auth'];

self.addEventListener('install', (event) => {
  event.waitUntil((async () => {
    const cache = await caches.open(STATIC_CACHE);
    // 오프라인 화면에 관리자 정보가 들어가지 않게 쿠키 없이 받는다.
    await cache.addAll(PRECACHE.map((url) => new Request(url, { credentials: 'omit' })));
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keep = [STATIC_CACHE, PAGE_CACHE];
    for (const key of await caches.keys()) {
      if (!keep.includes(key)) await caches.delete(key);
    }
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (req.method !== 'GET' || url.origin !== self.location.origin) return;
  if (BYPASS.some((p) => url.pathname === p || url.pathname.startsWith(p + '/'))) return;
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(cacheFirst(req));
    return;
  }
  // 주소창 이동과 hx-boost 이동은 전체 화면을 받으므로 캐시해 두고 오프라인일 때 보여 준다.
  // 목록 조각 같은 다른 HTMX 요청은 같은 주소라도 응답이 달라서 건드리지 않는다.
  if (req.mode === 'navigate' || req.headers.get('HX-Boosted') === 'true') {
    event.respondWith(networkFirst(req, url.href));
  }
});

async function cacheFirst(req) {
  const cached = await caches.match(req);
  if (cached) return cached;
  const res = await fetch(req);
  if (res.ok) {
    const cache = await caches.open(STATIC_CACHE);
    await cache.put(req, res.clone());
  }
  return res;
}

async function networkFirst(req, key) {
  try {
    const res = await fetch(req);
    if (cacheable(res)) {
      const cache = await caches.open(PAGE_CACHE);
      await cache.put(key, res.clone());
      await trim(cache);
    }
    return res;
  } catch (err) {
    const cached = await caches.match(key, { cacheName: PAGE_CACHE });
    if (cached) return cached;
    const offline = await caches.match('/offline', { cacheName: STATIC_CACHE });
    if (offline) return offline;
    throw err;
  }
}

// 로그인한 관리자에게 보인 화면은 서버가 no-store를 붙이므로 캐시하지 않는다.
function cacheable(res) {
  return res.ok && res.type === 'basic' && !(res.headers.get('Cache-Control') || '').includes('no-store');
}

async function trim(cache) {
  const keys = await cache.keys();
  for (const key of keys.slice(0, Math.max(0, keys.length - MAX_PAGES))) {
    await cache.delete(key);
  }
}
