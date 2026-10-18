BIN := tlc
PREFIX ?= $(HOME)/.local

.PHONY: build install test vet fmt tidy clean

build:
	go build -trimpath -o bin/$(BIN) ./cmd/tlc

install:
	go build -trimpath -o $(PREFIX)/bin/$(BIN) ./cmd/tlc

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin
