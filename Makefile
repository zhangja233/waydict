GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BIN ?= waydict
BUILD_TAGS ?= sherpa pipewire whispercpp
GO_ENV ?= CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow
VERSION ?= 0.1.0
BUILD_NUMBER ?= 1
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_TAGS_MACOS ?= coreaudio
empty :=
space := $(empty) $(empty)
comma := ,
MACOS_BUILD_TAGS_METADATA := $(subst $(space),$(comma),$(strip $(BUILD_TAGS_MACOS)))
MACOS_APP := build/Waydict.app
MACOS_CONTENTS := $(MACOS_APP)/Contents
MACOS_LDFLAGS := -s -w -X waydict/internal/buildinfo.Version=$(VERSION) -X waydict/internal/buildinfo.Commit=$(COMMIT) -X waydict/internal/buildinfo.BuildNumber=$(BUILD_NUMBER) -X waydict/internal/buildinfo.BuildTags=$(MACOS_BUILD_TAGS_METADATA)

.PHONY: build build-macos-dev test test-macos-bundle test-sherpa test-pipewire test-model test-whisper install clean

build:
	$(GO_ENV) $(GO) build -tags "$(BUILD_TAGS)" -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/waydict

build-macos-dev:
	$(RM) -r $(MACOS_APP)
	install -d $(MACOS_CONTENTS)/MacOS $(MACOS_CONTENTS)/Resources/en.lproj $(MACOS_CONTENTS)/Frameworks
	$(GO_ENV) $(GO) build -tags "$(BUILD_TAGS_MACOS)" -trimpath -ldflags "$(MACOS_LDFLAGS)" -o $(MACOS_CONTENTS)/MacOS/waydict-app ./cmd/waydict-app
	$(GO_ENV) $(GO) build -tags "$(BUILD_TAGS_MACOS)" -trimpath -ldflags "$(MACOS_LDFLAGS)" -o $(MACOS_CONTENTS)/MacOS/waydict ./cmd/waydict
	sed -e 's|@VERSION@|$(VERSION)|g' -e 's|@BUILD_NUMBER@|$(BUILD_NUMBER)|g' packaging/macos/Info.plist.in > $(MACOS_CONTENTS)/Info.plist
	cp packaging/macos/Resources/en.lproj/Localizable.strings packaging/macos/Resources/en.lproj/InfoPlist.strings $(MACOS_CONTENTS)/Resources/en.lproj/
	cp packaging/macos/Resources/model-catalog.json $(MACOS_CONTENTS)/Resources/
	cp packaging/macos/README.txt packaging/macos/THIRD_PARTY_NOTICES.md LICENSE $(MACOS_CONTENTS)/Resources/
	$(GO) run ./scripts/macos/icon -output $(MACOS_CONTENTS)/Resources/Waydict.icns
	printf 'APPL????' > $(MACOS_CONTENTS)/PkgInfo
	plutil -lint $(MACOS_CONTENTS)/Info.plist
	plutil -lint $(MACOS_CONTENTS)/Resources/en.lproj/Localizable.strings $(MACOS_CONTENTS)/Resources/en.lproj/InfoPlist.strings
	codesign -s - --force --options runtime --entitlements packaging/macos/Waydict.entitlements $(MACOS_CONTENTS)/MacOS/waydict
	codesign -s - --force --options runtime --entitlements packaging/macos/Waydict.entitlements $(MACOS_CONTENTS)/MacOS/waydict-app
	codesign -s - --force --options runtime --entitlements packaging/macos/Waydict.entitlements $(MACOS_APP)

test-macos-bundle:
	$(GO) test ./packaging/macos

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
	$(RM) -r $(MACOS_APP)
