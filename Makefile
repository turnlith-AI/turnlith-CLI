BIN := tlc
PREFIX ?= $(HOME)/.local

.PHONY: build install test race vet fmt tidy cross check clean

build:
	go build -trimpath -o bin/$(BIN) ./cmd/tlc

install:
	go build -trimpath -o $(PREFIX)/bin/$(BIN) ./cmd/tlc

test:
	go test ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

# vet every release target: the windows and arm builds are only type-checked
# here, so a platform-specific break shows up before CI
cross:
	GOOS=windows GOARCH=amd64 go vet ./...
	GOOS=windows GOARCH=arm64 go vet ./...
	GOOS=darwin GOARCH=arm64 go vet ./...
	GOOS=linux GOARCH=arm64 go vet ./...

# what CI runs
check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	go test -race -count=1 ./...
	$(MAKE) cross
	go mod tidy && git diff --exit-code -- go.mod go.sum

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin
