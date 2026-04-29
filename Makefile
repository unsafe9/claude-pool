build:
	go build -o bin/claudeproxy ./cmd/claudeproxy

test:
	go test ./...

run:
	go run ./cmd/claudeproxy
