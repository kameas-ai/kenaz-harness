# kenaz-harness Makefile
#
# Phase 1 adds the harness-vm stub targets. Existing build processes for the
# Wails desktop app use wails build directly (not captured here). Do not add
# targets that conflict with wails build invocations.
#
# New targets only — do not modify existing build processes.

GO  := go
BIN := ./bin

.PHONY: build-harness-vm build-harness-vm-linux-arm64 help-phase1 test-browser-tool

## ---------- Phase 1: headless image targets --------------------------------

## build-harness-vm: compile the in-VM harness stub binary for the headless
## guest image (Phase 1). Produces bin/kenaz-harness-vm for the current host.
build-harness-vm: | $(BIN)
	$(GO) build -o $(BIN)/kenaz-harness-vm ./cmd/harness-vm/

## build-harness-vm-linux-arm64: cross-compile the in-VM harness stub for
## Linux/arm64 (Tart Apple Silicon guest). Used by the bake script.
build-harness-vm-linux-arm64: | $(BIN)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build \
	  -o $(BIN)/kenaz-harness-vm-linux-arm64 \
	  ./cmd/harness-vm/

$(BIN):
	@mkdir -p $(BIN)

## help-phase1: show Phase 1 targets.
help-phase1:
	@echo "kenaz-harness Phase 1 targets:"
	@echo "  build-harness-vm              Build in-VM harness stub (current arch)"
	@echo "  build-harness-vm-linux-arm64  Cross-compile for Linux/arm64 (Tart)"

## ---------- Phase 10: browser tool targets --------------------------------

## test-browser-tool: run unit tests for the kaneaz__browser CDP tool.
## Tests use a minimal httptest CDP fake (no real chromium required).
## Runs with -race to catch any concurrency issues in the session serializer.
test-browser-tool:
	$(GO) test -count=1 -race ./core/tools/browser/...
	@echo "test-browser-tool: passed"
