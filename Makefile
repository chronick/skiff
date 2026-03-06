.PHONY: build test test-cover test-cover-html clean

build:
	go build -o plane ./cmd/plane

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
