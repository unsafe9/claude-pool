build:
	go build -o bin/claude-pool ./cmd/claude-pool

test:
	go test ./...

# Install a dev build into ~/.local/bin — the same location the installers and
# self-update use, so hooks pick it up via PATH. Dev builds report version
# "dev" and are never auto-replaced.
install:
	mkdir -p $(HOME)/.local/bin
	go build -o $(HOME)/.local/bin/claude-pool ./cmd/claude-pool
