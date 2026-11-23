BINARY  := vmono
PKG     := ./cmd/vmono
OUT     := bin/$(BINARY)
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/doneill612/vmono/internal/buildinfo.version=$(VERSION)

.PHONY: build install uninstall test vet snapshot clean

## build: compile the binary to bin/vmono, stamped with the git version
build:
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT) $(PKG)

## install: build, then copy the binary to $(BINDIR) (default ~/.local/bin)
install: build
	install -d $(BINDIR)
	install -m 0755 $(OUT) $(BINDIR)/$(BINARY)
	@echo "installed $(BINDIR)/$(BINARY)"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "note: $(BINDIR) is not on your PATH" ;; esac

## uninstall: remove the installed binary
uninstall:
	rm -f $(BINDIR)/$(BINARY)

## test: run vet and the test suite
test: vet
	go test ./...

vet:
	go vet ./...

## snapshot: dry-run the release build into dist/ (needs goreleaser)
snapshot:
	goreleaser release --snapshot --clean

## clean: remove build output
clean:
	rm -rf bin dist
