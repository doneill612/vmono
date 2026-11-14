BINARY  := vmono
PKG     := ./cmd/vmono
OUT     := bin/$(BINARY)
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin

.PHONY: build install uninstall test vet clean

## build: compile the binary to bin/vmono
build:
	@mkdir -p bin
	go build -trimpath -o $(OUT) $(PKG)

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

## clean: remove build output
clean:
	rm -rf bin
