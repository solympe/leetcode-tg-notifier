.PHONY: build test test-integration lint generate tidy

build:
	go build ./...

test:
	go test ./... -race -count=1

test-integration:
	go test -tags integration -race -count=1 -timeout 5m ./...

lint:
	golangci-lint run ./...

generate:
	PATH="$$(go env GOPATH)/bin:$$PATH" go generate ./...

tidy:
	go mod tidy
