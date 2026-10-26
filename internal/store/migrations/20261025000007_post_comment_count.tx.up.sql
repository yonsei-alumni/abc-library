-- 밴드 화면에 나온 게시글의 댓글 수. 배치가 '최신 댓글순' 목록의 댓글 수와 비교해서 새 댓글이 달린 글을 찾는다.
-- 이 컬럼을 더하기 전에 저장한 글은 NULL(모름)이고, 다음에 그 글을 읽을 때 채운다.
ALTER TABLE posts ADD COLUMN comment_count integer;
