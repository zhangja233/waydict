GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BIN ?= sway-voice
BUILD_TAGS ?= sherpa pipewire
GO_ENV ?= CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow

.PHONY: build test test-sherpa test-pipewire test-model install clean

build:
	$(GO_ENV) $(GO) build -tags "$(BUILD_TAGS)" -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/sway-voice

test:
	$(GO) test ./...

test-sherpa:
	$(GO_ENV) $(GO) test -tags sherpa ./internal/asr/sherpa ./internal/vad

test-pipewire:
	SWAY_VOICE_TEST_PIPEWIRE=1 $(GO_ENV) $(GO) test -tags pipewire ./internal/audio/pipewire

test-model: build
	$(GO_ENV) $(GO) test -tags sherpa ./internal/asr/sherpa
	./$(BIN) model check
	test -n "$$SWAY_VOICE_TEST_WAV"
	test -n "$$(./$(BIN) transcribe --file "$$SWAY_VOICE_TEST_WAV")"
	./$(BIN) bench --file "$$SWAY_VOICE_TEST_WAV"

install: build
	install -Dm755 $(BIN) $(BINDIR)/$(BIN)
	mkdir -p $(HOME)/.config/sway-voice
	cp -n testdata/sample-config.toml $(HOME)/.config/sway-voice/config.toml

clean:
	$(GO) clean
	$(RM) $(BIN)
