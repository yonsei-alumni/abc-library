-- 잡지처럼 ISBN-13이 없는 교보문고 상품의 바코드. 교보문고 표지 이미지는 상품 번호가 아니라 바코드로 찾는다.
ALTER TABLE books ADD COLUMN kyobo_barcode text;
