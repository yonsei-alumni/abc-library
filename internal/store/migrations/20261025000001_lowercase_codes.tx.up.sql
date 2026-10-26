-- 공개 주소의 코드를 영어 소문자 3자로 다시 뽑는다. 예전 주소(대소문자와 숫자 8자 또는 3자)는 쓸 수 없게 된다.
DO $$
DECLARE
  r record;
  c text;
BEGIN
  FOR r IN SELECT id FROM bookmark_categories ORDER BY id LOOP
    LOOP
      c := '';
      FOR i IN 1..3 LOOP
        c := c || chr(97 + floor(random() * 26)::int);
      END LOOP;
      EXIT WHEN NOT EXISTS (SELECT 1 FROM bookmark_categories WHERE code = c);
    END LOOP;
    UPDATE bookmark_categories SET code = c WHERE id = r.id;
  END LOOP;
END $$;
