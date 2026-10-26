-- 밴드 게시글. 본문과 작성자는 저장하지 않고, 바뀌었는지 알기 위한 해시만 둔다.
CREATE TABLE posts (
  id bigserial PRIMARY KEY,
  band_post_no integer NOT NULL UNIQUE,
  posted_at timestamptz NOT NULL,
  content_hash text NOT NULL,
  processed_at timestamptz NOT NULL DEFAULT now()
);

--bun:split

-- 책. isbn13이 있으면 그것으로, 없으면 work_key(정규화한 제목과 대표 저자)로 같은 책을 찾는다.
CREATE TABLE books (
  id bigserial PRIMARY KEY,
  isbn13 text UNIQUE,
  work_key text NOT NULL UNIQUE,
  title text NOT NULL,
  authors text[] NOT NULL DEFAULT '{}',
  publisher text NOT NULL DEFAULT '',
  kyobo_pid text,
  genres text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

--bun:split

CREATE INDEX books_authors_idx ON books USING gin (authors);

--bun:split

CREATE INDEX books_genres_idx ON books USING gin (genres);

--bun:split

-- 게시글이 책에 대해 한 이야기의 AI 요약과 책 관련 링크.
CREATE TABLE book_posts (
  book_id bigint NOT NULL REFERENCES books (id) ON DELETE CASCADE,
  post_id bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  summary text NOT NULL,
  links jsonb NOT NULL DEFAULT '[]',
  PRIMARY KEY (book_id, post_id)
);

--bun:split

CREATE INDEX book_posts_post_id_idx ON book_posts (post_id);
