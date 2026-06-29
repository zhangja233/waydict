GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BIN ?= sway-voice
BUILD_TAGS ?= sherpa pipewire
GO_ENV ?= CGO_ENABLED=1

.PHONY: build test test-sherpa test-pipewire test-model install clean

build:
	$(GO_ENV) $(GO) build -tags "$(BUILD_TAGS)" -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/sway-voice

test:
	$(GO) test ./...

test-sherpa:
	$(GO_ENV) $(GO) test -tags sherpa ./internal/asr/sherpa ./internal/vad

test-pipewire:
	SWAY_VOICE_TEST_PIPEWIRE=1 $(GO_ENV) $(GO) test -tags pipewire ./internal/audio/pipewire

test-model:
	$(GO_ENV) $(GO) test -tags sherpa ./internal/asr/sherpa
	./$(BIN) model check

install: build
	install -Dm755 $(BIN) $(BINDIR)/$(BIN)
	mkdir -p $(HOME)/.config/sway-voice
	cp -n testdata/sample-config.toml $(HOME)/.config/sway-voice/config.toml

clean:
	$(GO) clean
	$(RM) $(BIN)
