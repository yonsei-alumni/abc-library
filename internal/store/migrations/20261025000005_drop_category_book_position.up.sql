-- 카테고리 안의 책은 북마크 순서(bookmarks.position)를 따르므로 카테고리별 순서는 쓰지 않는다.
ALTER TABLE bookmark_category_books DROP COLUMN position;
