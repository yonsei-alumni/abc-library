DROP TABLE admins;

--bun:split

ALTER TABLE mentions DROP COLUMN hidden;

--bun:split

ALTER TABLE mentions DROP COLUMN edited;

--bun:split

ALTER TABLE books DROP COLUMN edited_at;

--bun:split

ALTER TABLE books ADD COLUMN work_key text;

--bun:split

-- 책마다 키 하나를 작품 키로 되돌린다. 'isbn:' 키는 버린다.
UPDATE books b SET work_key = coalesce(
  (SELECT min(k.key) FROM book_keys k WHERE k.book_id = b.id AND k.key NOT LIKE 'isbn:%'),
  'book:' || b.id);

--bun:split

ALTER TABLE books ALTER COLUMN work_key SET NOT NULL;

--bun:split

ALTER TABLE books ADD CONSTRAINT books_work_key_key UNIQUE (work_key);

--bun:split

DROP TABLE book_keys;

--bun:split

ALTER TABLE book_tags DROP CONSTRAINT book_tags_tag_fkey;

--bun:split

DROP TABLE tags;
