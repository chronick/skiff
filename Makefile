.PHONY: build test test-cover test-cover-html clean install uninstall

PREFIX ?= /usr/local

build:
	go build -o plane ./cmd/plane

install: build
	install -d $(PREFIX)/bin
	install -m 755 plane $(PREFIX)/bin/plane

uninstall:
	rm -f $(PREFIX)/bin/plane

test:
	go test ./...

test-cover:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

test-cover-html: test-cover
	go tool cover -html=coverage.out -o coverage.html
	open coverage.html

clean:
	rm -f plane coverage.out coverage.html
