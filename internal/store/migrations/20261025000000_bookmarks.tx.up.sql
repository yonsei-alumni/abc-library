-- 로그인한 사용자. 메일 주소, 이름, 프로필은 저장하지 않는다.
-- subject_hash는 Google 계정 고유 번호(sub)를 서버의 비밀 키로 HMAC-SHA256한 값이다. 이 값만으로는 어느 계정인지 알 수 없다.
CREATE TABLE users (
  id bigserial PRIMARY KEY,
  subject_hash text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);

--bun:split

-- 사용자가 관심 있다고 표시한 책.
CREATE TABLE bookmarks (
  user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  book_id bigint NOT NULL REFERENCES books (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, book_id)
);

--bun:split

CREATE INDEX bookmarks_book_idx ON bookmarks (book_id);

--bun:split

-- 사용자가 북마크를 묶는 카테고리. body는 Markdown이다.
-- public이 참이면 code로 만든 짧은 주소(/s/{code})에서 public_title(비었으면 title), body, 책 목록을 누구나 본다.
CREATE TABLE bookmark_categories (
  id bigserial PRIMARY KEY,
  user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  title text NOT NULL,
  body text NOT NULL DEFAULT '',
  public_title text NOT NULL DEFAULT '',
  public boolean NOT NULL DEFAULT false,
  code text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, user_id)
);

--bun:split

CREATE INDEX bookmark_categories_user_idx ON bookmark_categories (user_id);

--bun:split

-- 북마크 하나는 같은 사용자의 카테고리 여러 개에 들어갈 수 있다.
-- user_id를 함께 두어서 다른 사용자의 카테고리나 북마크와 이어지지 않게 한다.
CREATE TABLE bookmark_category_books (
  category_id bigint NOT NULL,
  user_id bigint NOT NULL,
  book_id bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (category_id, book_id),
  FOREIGN KEY (category_id, user_id) REFERENCES bookmark_categories (id, user_id) ON DELETE CASCADE,
  FOREIGN KEY (user_id, book_id) REFERENCES bookmarks (user_id, book_id) ON DELETE CASCADE
);

--bun:split

CREATE INDEX bookmark_category_books_bookmark_idx ON bookmark_category_books (user_id, book_id);
