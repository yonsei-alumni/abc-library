-- 카테고리 안에서 책의 순서. 작은 값이 먼저 온다. 순서는 카테고리마다 따로 바꾸고, 공개 화면도 이 순서를 따른다.
-- 처음에는 그때까지 보이던 순서(내 북마크의 순서)로 채운다.
ALTER TABLE bookmark_category_books ADD COLUMN position integer NOT NULL DEFAULT 0;

--bun:split

UPDATE bookmark_category_books cb SET position = o.rn
FROM (
  SELECT cb.category_id, cb.book_id,
         row_number() OVER (PARTITION BY cb.category_id ORDER BY k.position, k.created_at DESC, cb.book_id) AS rn
  FROM bookmark_category_books cb JOIN bookmarks k ON k.user_id = cb.user_id AND k.book_id = cb.book_id
) o
WHERE cb.category_id = o.category_id AND cb.book_id = o.book_id;
