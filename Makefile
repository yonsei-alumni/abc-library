.PHONY: fmt lint test check ci test-integration web migrate band-login batch status

fmt:
	goimports -w .

lint:
	golangci-lint run ./...

test:
	go test ./...

check: fmt lint test

ci: lint test

# 실제 Neon DB(임시 스키마)와 Claude API를 호출한다. .envrc의 속성이 필요하다.
test-integration:
	go test -tags integration -count=1 ./...

web:
	go run ./cmd/web

migrate:
	go run ./cmd/batch migrate

band-login:
	go run ./cmd/batch login

# 예: make batch ARGS="-limit 0 -redo-from 1"
batch:
	go run ./cmd/batch run $(ARGS)

status:
	go run ./cmd/batch status
