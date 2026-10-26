DROP FUNCTION squash(text);

--bun:split

CREATE TABLE book_posts (
  book_id bigint NOT NULL REFERENCES books (id) ON DELETE CASCADE,
  post_id bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  summary text NOT NULL,
  links jsonb NOT NULL DEFAULT '[]',
  PRIMARY KEY (book_id, post_id)
);

--bun:split

CREATE INDEX book_posts_post_id_idx ON book_posts (post_id);

--bun:split

INSERT INTO book_posts (book_id, post_id, summary, links)
SELECT DISTINCT ON (book_id, post_id) book_id, post_id, summary, links
FROM mentions WHERE kind = 'post'
ORDER BY book_id, post_id, mentioned_at;

--bun:split

DROP TABLE mentions;

--bun:split

ALTER TABLE books ADD COLUMN genres text[] NOT NULL DEFAULT '{}';

--bun:split

UPDATE books b SET genres = ARRAY(SELECT tag FROM book_tags t WHERE t.book_id = b.id);

--bun:split

DROP TABLE book_tags;
