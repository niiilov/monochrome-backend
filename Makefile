ifeq ($(OS),Windows_NT)
SH := C:/PROGRA~1/Git/usr/bin/sh.exe
else
SH := sh
endif

.PHONY: build test vet lint tidy run status test-mandatory check test-live

build:
	go build -o bin/digest ./cmd/digest
	go build -o bin/digestd ./cmd/digestd

test:
	go test ./...

vet:
	go vet ./...

lint:
	go vet ./...

tidy:
	go mod tidy

run:
	go run ./cmd/digest run

status:
	$(SH) scripts/status.sh

test-mandatory:
	go run ./cmd/digest test-mandatory

check:
	go run ./cmd/digest check

test-live:
	go test -tags live ./...
