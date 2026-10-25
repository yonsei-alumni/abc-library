-- 카테고리 안에서 책의 순서. 작은 값이 먼저 온다. 처음에는 넣은 순서의 역순(최근에 넣은 책이 먼저)으로 채운다.
ALTER TABLE bookmark_category_books ADD COLUMN position integer NOT NULL DEFAULT 0;

--bun:split

UPDATE bookmark_category_books cb SET position = o.rn
FROM (
  SELECT category_id, book_id, row_number() OVER (PARTITION BY category_id ORDER BY created_at DESC, book_id) AS rn
  FROM bookmark_category_books
) o
WHERE cb.category_id = o.category_id AND cb.book_id = o.book_id;
