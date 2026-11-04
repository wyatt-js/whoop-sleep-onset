AWS_PROFILE ?= whoop

.PHONY: build-api build-sync build-cli check-backend protect-backend deploy-backend smoke-backend

build-api:
	mkdir -p bin/api
	GOOS=linux GOARCH=arm64 go build -o bin/api/bootstrap ./cmd/lambda-api
	zip -j bin/api/lambda-api.zip bin/api/bootstrap

build-sync:
	mkdir -p bin/sync
	GOOS=linux GOARCH=arm64 go build -o bin/sync/bootstrap ./cmd/lambda-sync
	zip -j bin/sync/lambda-sync.zip bin/sync/bootstrap

build-cli:
	mkdir -p bin
	go build -o bin/sleeponset ./cmd/sleeponset

check-backend:
	python3 scripts/backend.py check --profile $(AWS_PROFILE)

protect-backend:
	python3 scripts/backend.py protect --profile $(AWS_PROFILE)

deploy-backend:
	python3 scripts/backend.py deploy --profile $(AWS_PROFILE)

smoke-backend:
	python3 scripts/backend.py smoke-data --profile $(AWS_PROFILE)
