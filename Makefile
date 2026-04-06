build-api:
	GOOS=linux GOARCH=arm64 go build -o bin/api/bootstrap ./cmd/lambda-api

build-sync:
	GOOS=linux GOARCH=arm64 go build -o bin/sync/bootstrap ./cmd/lambda-sync

build-cli:
	go build -o bin/sleeponset ./cmd/sleeponset