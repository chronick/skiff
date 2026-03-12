.PHONY: build test test-cover test-cover-html clean install uninstall

PREFIX ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)

build:
	go build -ldflags="$(LDFLAGS)" -o skiff ./cmd/skiff
	go build -o skiff-menu ./cmd/skiff-menu

install: build
	install -d $(PREFIX)/bin
	install -m 755 skiff $(PREFIX)/bin/skiff
	install -m 755 skiff-menu $(PREFIX)/bin/skiff-menu

uninstall:
	rm -f $(PREFIX)/bin/skiff $(PREFIX)/bin/skiff-menu

test:
	go test ./...

test-cover:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

test-cover-html: test-cover
	go tool cover -html=coverage.out -o coverage.html
	open coverage.html

clean:
	rm -f skiff skiff-menu coverage.out coverage.html
