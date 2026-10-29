set positional-arguments

default:
    just --list

# Install this clone's git hooks: commit messages must be Conventional Commits.
hooks:
    git config core.hooksPath .githooks
    @echo "Installed .githooks (commit-msg)."

build:
    mkdir -p bin
    go build -trimpath -o bin/cake-suggest ./cmd/cake-suggest

version-check: build
    ./bin/cake-suggest -version | grep -q '^revision '

install:
    go install -trimpath ./cmd/cake-suggest

test:
    go test ./...

test-race:
    go test -race ./...

fmt:
    gofmt -w .

fmt-check:
    test -z "$(gofmt -l .)"

vet:
    go vet ./...

ci: fmt-check vet test build version-check
