build-api:
	GOOS=linux GOARCH=arm64 go build -o bin/api/bootstrap ./cmd/lambda-api
	zip -j bin/api/lambda-api.zip bin/api/bootstrap

build-sync:
	GOOS=linux GOARCH=arm64 go build -o bin/sync/bootstrap ./cmd/lambda-sync
	zip -j bin/sync/lambda-sync.zip bin/sync/bootstrap

build-cli:
	go build -o bin/sleeponset ./cmd/sleeponset