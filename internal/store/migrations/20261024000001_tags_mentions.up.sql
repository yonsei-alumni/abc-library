-- 책 한 권에 태그 여러 개(1:N). 장르와 모임 주제를 함께 담는다.
CREATE TABLE book_tags (
  book_id bigint NOT NULL REFERENCES books (id) ON DELETE CASCADE,
  tag text NOT NULL,
  PRIMARY KEY (book_id, tag)
);

--bun:split

CREATE INDEX book_tags_tag_idx ON book_tags (tag);

--bun:split

INSERT INTO book_tags (book_id, tag)
SELECT id, unnest(genres) FROM books
ON CONFLICT DO NOTHING;

--bun:split

ALTER TABLE books DROP COLUMN genres;

--bun:split

-- 게시글 본문이나 댓글 하나가 책에 대해 한 이야기의 AI 요약. 책 상세 화면에 댓글처럼 쌓인다.
CREATE TABLE mentions (
  id bigserial PRIMARY KEY,
  book_id bigint NOT NULL REFERENCES books (id) ON DELETE CASCADE,
  post_id bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('post', 'comment')),
  mentioned_at timestamptz NOT NULL,
  summary text NOT NULL,
  links jsonb NOT NULL DEFAULT '[]'
);

--bun:split

CREATE INDEX mentions_book_idx ON mentions (book_id, mentioned_at);

--bun:split

CREATE INDEX mentions_post_idx ON mentions (post_id);

--bun:split

INSERT INTO mentions (book_id, post_id, kind, mentioned_at, summary, links)
SELECT bp.book_id, bp.post_id, 'post', p.posted_at, bp.summary, bp.links
FROM book_posts bp JOIN posts p ON p.id = bp.post_id;

--bun:split

DROP TABLE book_posts;

--bun:split

-- 검색어와 제목, 저자, 태그를 대소문자와 공백을 무시하고 비교하기 위한 정규화 함수.
CREATE FUNCTION squash(s text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE
AS $$ SELECT regexp_replace(lower(s), '\s', '', 'g') $$;
