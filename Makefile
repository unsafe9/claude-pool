build:
	go build -o bin/claude-pool ./cmd/claude-pool

test:
	go test ./...

install:
	go install ./cmd/claude-pool
