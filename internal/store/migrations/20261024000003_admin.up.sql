-- 책에 붙일 수 있는 태그. 관리 화면에서 더하고 지운다.
-- kind는 topic(모임 주제)이나 kind(책의 갈래)다. 같은 갈래 안에서는 position이 작은 것이 먼저 온다.
CREATE TABLE tags (
  name text PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('topic', 'kind')),
  position integer NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

--bun:split

INSERT INTO tags (name, kind, position)
SELECT name, 'topic', ord FROM unnest(ARRAY['AI', 'SF', '과학']) WITH ORDINALITY AS t (name, ord);

--bun:split

INSERT INTO tags (name, kind, position)
SELECT name, 'kind', ord FROM unnest(ARRAY[
  '소설', '추리·미스터리', '판타지', '시', '에세이',
  '인문', '철학', '역사', '사회·정치', '경제·경영', '자기계발',
  '기술·IT', '예술', '여행', '종교', '어린이·청소년', '만화'
]) WITH ORDINALITY AS t (name, ord);

--bun:split

-- 목록 밖의 태그가 남아 있으면 갈래 태그 맨 뒤에 더해서 잃지 않게 한다.
INSERT INTO tags (name, kind, position)
SELECT tag, 'kind', 100 + row_number() OVER (ORDER BY tag)
FROM (SELECT DISTINCT tag FROM book_tags WHERE tag NOT IN (SELECT name FROM tags)) AS t;

--bun:split

-- 태그 이름을 바꾸면 책에 붙은 태그도 바뀌고, 태그를 지우면 책에서도 빠진다.
ALTER TABLE book_tags ADD CONSTRAINT book_tags_tag_fkey
  FOREIGN KEY (tag) REFERENCES tags (name) ON UPDATE CASCADE ON DELETE CASCADE;

--bun:split

-- 배치가 같은 책을 찾는 키. 작품 키(정규화한 제목과 대표 저자)와 'isbn:'을 붙인 ISBN을 담는다.
-- 관리자가 제목이나 ISBN을 고치거나 책을 합쳐도 예전 키가 남아서, 배치가 다시 추출해도 같은 책을 찾는다.
CREATE TABLE book_keys (
  key text PRIMARY KEY,
  book_id bigint NOT NULL REFERENCES books (id) ON DELETE CASCADE
);

--bun:split

CREATE INDEX book_keys_book_idx ON book_keys (book_id);

--bun:split

INSERT INTO book_keys (key, book_id) SELECT work_key, id FROM books;

--bun:split

INSERT INTO book_keys (key, book_id) SELECT 'isbn:' || isbn13, id FROM books WHERE isbn13 IS NOT NULL
ON CONFLICT DO NOTHING;

--bun:split

ALTER TABLE books DROP COLUMN work_key;

--bun:split

-- 관리자가 고친 책. 배치는 이 책의 정보와 태그를 바꾸지 않는다.
ALTER TABLE books ADD COLUMN edited_at timestamptz;

--bun:split

-- edited는 관리자가 고친 언급이다. 배치가 그 글을 다시 추출해도 지우지 않고,
-- 같은 본문이나 댓글에서 같은 책에 대한 새 언급을 더하지 않는다.
-- hidden은 관리자가 지운 언급이다. 화면에 보이지 않고, 배치가 같은 언급을 다시 만들지 않게 남겨 둔다.
ALTER TABLE mentions ADD COLUMN edited boolean NOT NULL DEFAULT false;

--bun:split

ALTER TABLE mentions ADD COLUMN hidden boolean NOT NULL DEFAULT false;

--bun:split

-- 관리자. Google 계정의 메일 주소로 구별한다.
CREATE TABLE admins (
  email text PRIMARY KEY,
  added_by text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

--bun:split

INSERT INTO admins (email) VALUES ('benelog@gmail.com');
