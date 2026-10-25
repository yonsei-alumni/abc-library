-- 태그는 모임 주제인지만 가린다. 모임 주제가 아닌 태그는 소설, 인문 같은 책의 갈래다.
-- 모임 주제끼리, 그 밖의 태그끼리 position이 작은 것이 먼저 온다.
ALTER TABLE tags ADD COLUMN topic boolean NOT NULL DEFAULT false;

--bun:split

UPDATE tags SET topic = (kind = 'topic');

--bun:split

ALTER TABLE tags DROP COLUMN kind;
