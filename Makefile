.PHONY: fmt lint test check ci test-integration web migrate band-login batch status \
	gcp-setup gcp-schedule band-secret deploy-web deploy-batch

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

# Google Cloud 배포. 값은 scripts/gcp-env.sh에 있다.
# 처음 한 번: make gcp-setup → make band-secret → make deploy-web deploy-batch → make gcp-schedule
gcp-setup:
	scripts/gcp-setup.sh

gcp-schedule:
	scripts/gcp-schedule.sh

# 밴드 세션이 만료되면 make band-login 뒤에 실행한다.
band-secret:
	scripts/band-secret.sh

deploy-web:
	scripts/deploy.sh web

deploy-batch:
	scripts/deploy.sh batch
