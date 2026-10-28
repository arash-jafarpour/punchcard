.PHONY: build install completion run test race vet fmt fmt-check check clean

BINARY  := bin/punchcard
PKG     := ./cmd/punchcard
GO      ?= go

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.buildVersion=$(VERSION) -X main.buildCommit=$(COMMIT) -X main.buildDate=$(DATE)

build:
	@$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@$(MAKE) --no-print-directory completion

install: build
	@$(GO) install -ldflags "$(LDFLAGS)" $(PKG)
	@dir="$$($(GO) env GOBIN)"; [ -n "$$dir" ] || dir="$$($(GO) env GOPATH)/bin"; \
	echo "installed $$dir/punchcard"

# Install shell completion for the current $SHELL (bash, zsh, or fish) into the
# shell's standard completion directory so it loads automatically.
completion:
	@if [ ! -x ./$(BINARY) ]; then echo "completion: ./$(BINARY) not found; run 'make build' first" >&2; exit 1; fi
	@shell="$$(basename "$${SHELL:-bash}")"; \
	case "$$shell" in \
		bash) dir="$${XDG_DATA_HOME:-$$HOME/.local/share}/bash-completion/completions"; file="punchcard";; \
		zsh)  dir="$$HOME/.zsh/completions"; file="_punchcard";; \
		fish) dir="$$HOME/.config/fish/completions"; file="punchcard.fish";; \
		*) echo "completion: unsupported shell '$$shell' (run './$(BINARY) completion <shell>' manually)"; exit 0;; \
	esac; \
	mkdir -p "$$dir"; \
	./$(BINARY) completion "$$shell" > "$$dir/$$file"; \
	echo "completion installed: $$dir/$$file"

# ARGS is forwarded to the binary: make run ARGS="--date 2026-10-27"
run: build
	@./$(BINARY) --dry-run $(ARGS)

test:
	@$(GO) test ./... -count=1

race:
	@$(GO) test -race ./... -count=1

vet:
	@$(GO) vet ./...

fmt:
	@gofmt -w .

# Fail when a file is not formatted. Use it in CI.
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [[ -n "$$out" ]]; then \
		echo "gofmt needed for:"; echo "$$out"; exit 1; \
	fi

check: fmt-check vet test

clean:
	@rm -rf bin
