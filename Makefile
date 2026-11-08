.PHONY: build test vet lint tidy run test-mandatory check test-live

build:
	go build -o bin/mono ./cmd/mono
	go build -o bin/monod ./cmd/monod

test:
	go test ./...

vet:
	go vet ./...

lint:
	go vet ./...

tidy:
	go mod tidy

run:
	go run ./cmd/mono run

test-mandatory:
	go run ./cmd/mono test-mandatory

check:
	go run ./cmd/mono check

test-live:
	go test -tags live ./...
