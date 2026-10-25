-- 사용자의 카테고리 순서. 작은 값이 먼저 온다. 처음에는 만든 순서로 채운다.
ALTER TABLE bookmark_categories ADD COLUMN position integer NOT NULL DEFAULT 0;

--bun:split

UPDATE bookmark_categories c SET position = o.rn
FROM (SELECT id, row_number() OVER (PARTITION BY user_id ORDER BY created_at, id) AS rn FROM bookmark_categories) o
WHERE c.id = o.id;
