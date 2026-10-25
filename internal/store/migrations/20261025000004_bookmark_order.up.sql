-- 사용자의 북마크 순서. 작은 값이 먼저 온다. 내 북마크와 모든 카테고리(공개 화면 포함)가 이 순서를 따른다.
-- 처음에는 그때까지 보이던 순서(최근에 북마크한 책이 먼저)로 채운다.
ALTER TABLE bookmarks ADD COLUMN position integer NOT NULL DEFAULT 0;

--bun:split

UPDATE bookmarks k SET position = o.rn
FROM (
  SELECT user_id, book_id, row_number() OVER (PARTITION BY user_id ORDER BY created_at DESC, book_id) AS rn
  FROM bookmarks
) o
WHERE k.user_id = o.user_id AND k.book_id = o.book_id;
