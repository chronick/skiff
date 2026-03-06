.PHONY: build test test-cover test-cover-html clean install uninstall

PREFIX ?= /usr/local

build:
	go build -o plane ./cmd/plane
	go build -o plane-menu ./cmd/plane-menu

install: build
	install -d $(PREFIX)/bin
	install -m 755 plane $(PREFIX)/bin/plane
	install -m 755 plane-menu $(PREFIX)/bin/plane-menu

uninstall:
	rm -f $(PREFIX)/bin/plane $(PREFIX)/bin/plane-menu

test:
	go test ./...

test-cover:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

test-cover-html: test-cover
	go tool cover -html=coverage.out -o coverage.html
	open coverage.html

clean:
	rm -f plane plane-menu coverage.out coverage.html
