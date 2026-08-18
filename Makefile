RED=\033[31m
GREEN=\033[32m
RESET=\033[0m

COLORIZE_PASS = sed "s/^\([- ]*\)\(PASS\)/\1$$(printf "$(GREEN)")\2$$(printf "$(RESET)")/g"
COLORIZE_FAIL = sed "s/^\([- ]*\)\(FAIL\)/\1$$(printf "$(RED)")\2$$(printf "$(RESET)")/g"

VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)
LDFLAGS := -s -w -X 'main.version=$(VERSION)'
GO_FILES := $(shell find . -type f -name '*.go' -print)

TEST_FULL_RESULT := "$$(go test -race -cover -v ./... -coverpkg=./...)"
TEST_COV_RESULT := "$$(go test -race -cover -v ./... -coverpkg=./... -coverprofile=cover.out.tmp)"

FAIL_CHECK := "^[^\s\t]*FAIL[^\s\t]*$$"

.PHONY: test test_view lint run build install clean

test:
	@! echo $(TEST_FULL_RESULT) | $(COLORIZE_PASS) | $(COLORIZE_FAIL) | tee /dev/stderr | grep $(FAIL_CHECK) > /dev/null

test_view:
	@! echo $(TEST_COV_RESULT) | $(COLORIZE_PASS) | $(COLORIZE_FAIL) | tee /dev/stderr | grep $(FAIL_CHECK) > /dev/null
	cat cover.out.tmp > cover.out
	rm cover.out.tmp
	go tool cover -func=cover.out
	go tool cover -html=cover.out -o cover.html

# Pinned rather than "whatever is on $PATH": .golangci.yml is v2-format,
# so a v1 binary exits with a config error and lints nothing. That failure
# reads like a repo problem, not "your lint did not run", which is the
# wrong way for this to fail given CLAUDE.md requires it before pushing.
# Keep in step with golangci_lint_version in .github/workflows/ci.yml.
GOLANGCI_LINT_VERSION := v2.12.2

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

run:
	go mod tidy
	go run -ldflags "$(LDFLAGS)" ./cmd/markgate $${OPT}

build: $(GO_FILES)
	go mod tidy
	go build -ldflags "$(LDFLAGS)" -o markgate ./cmd/markgate

install:
	go install -ldflags "$(LDFLAGS)" github.com/go-to-k/markgate/cmd/markgate

clean:
	go clean
	rm -f markgate cover.out cover.out.tmp cover.html
