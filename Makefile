GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BIN ?= waydict
BUILD_TAGS ?= sherpa pipewire whispercpp
GO_ENV ?= CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow

.PHONY: build test test-sherpa test-pipewire test-model test-whisper install clean

build:
	$(GO_ENV) $(GO) build -tags "$(BUILD_TAGS)" -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/waydict

test:
	$(GO) test ./...

test-sherpa:
	$(GO_ENV) $(GO) test -tags sherpa ./internal/asr/sherpa ./internal/vad

test-pipewire:
	WAYDICT_TEST_PIPEWIRE=1 $(GO_ENV) $(GO) test -tags pipewire ./internal/audio/pipewire

test-model: build
	$(GO_ENV) $(GO) test -tags sherpa ./internal/asr/sherpa
	./$(BIN) model check
	test -n "$$WAYDICT_TEST_WAV"
	test -n "$$(./$(BIN) transcribe --file "$$WAYDICT_TEST_WAV")"
	./$(BIN) bench --file "$$WAYDICT_TEST_WAV"

test-whisper:
	$(GO_ENV) $(GO) test -tags whispercpp ./internal/asr/whispercpp

install: build
	install -Dm755 $(BIN) $(BINDIR)/$(BIN)
	mkdir -p $(HOME)/.config/waydict
	cp -n testdata/sample-config.toml $(HOME)/.config/waydict/config.toml

clean:
	$(GO) clean
	$(RM) $(BIN)
