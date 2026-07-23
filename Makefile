.PHONY: build test lint clean release

build:
	go build -o bin/qingest ./cmd/qingest
	go build -o bin/qquery ./cmd/qquery

release:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/qingest ./cmd/qingest
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/qquery ./cmd/qquery

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
