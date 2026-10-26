-- 배치 실행 기록. 실행 전 저장된 마지막 글, 밴드 최신 글과 처리 결과를 남긴다.
CREATE TABLE batch_runs (
  id bigserial PRIMARY KEY,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  last_post_no integer NOT NULL DEFAULT 0,
  latest_post_no integer NOT NULL DEFAULT 0,
  checked integer NOT NULL DEFAULT 0,
  extracted integer NOT NULL DEFAULT 0,
  unchanged integer NOT NULL DEFAULT 0,
  missing integer NOT NULL DEFAULT 0,
  failed integer NOT NULL DEFAULT 0,
  books integer NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT ''
);

--bun:split

-- 처리하지 못한 글. 다음 실행에서 다시 시도하고, 처리하면 지운다.
-- reason은 failed(읽기, 추출, 저장 실패)나 missing(화면이 뜨지 않음. 삭제된 글이거나 일시적인 문제)이다.
CREATE TABLE pending_posts (
  band_post_no integer PRIMARY KEY,
  reason text NOT NULL CHECK (reason IN ('failed', 'missing')),
  attempts integer NOT NULL DEFAULT 1,
  last_error text NOT NULL DEFAULT '',
  last_tried_at timestamptz NOT NULL DEFAULT now()
);
