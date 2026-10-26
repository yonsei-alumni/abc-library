ALTER TABLE tags ADD COLUMN kind text NOT NULL DEFAULT 'kind' CHECK (kind IN ('topic', 'kind'));

--bun:split

UPDATE tags SET kind = CASE WHEN topic THEN 'topic' ELSE 'kind' END;

--bun:split

ALTER TABLE tags ALTER COLUMN kind DROP DEFAULT;

--bun:split

ALTER TABLE tags DROP COLUMN topic;
