.PHONY: build test lint generate tidy

build:
	go build ./...

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

generate:
	PATH="$$(go env GOPATH)/bin:$$PATH" go generate ./...

tidy:
	go mod tidy
