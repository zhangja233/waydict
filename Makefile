GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BIN ?= waydict
BUILD_TAGS ?= sherpa pipewire whispercpp
GO_ENV ?= CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow
VERSION ?= 0.1.0
BUILD_NUMBER ?= 1
COMMIT ?= $(shell bash scripts/macos/source-version.sh)
BUILD_TAGS_MACOS ?= coreaudio sherpa whispercpp
MACOS_ARCH ?= $(shell uname -m)
MACOS_GOARCH := $(if $(filter x86_64 amd64,$(MACOS_ARCH)),amd64,arm64)
MACOS_GO_ENV := CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow GOOS=darwin GOARCH=$(MACOS_GOARCH) MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS="-arch $(MACOS_ARCH) -mmacosx-version-min=13.0" CGO_CXXFLAGS="-arch $(MACOS_ARCH) -mmacosx-version-min=13.0" CGO_LDFLAGS="-arch $(MACOS_ARCH) -mmacosx-version-min=13.0"
empty :=
space := $(empty) $(empty)
comma := ,
MACOS_BUILD_TAGS_METADATA := $(subst $(space),$(comma),$(strip $(BUILD_TAGS_MACOS)))
MACOS_APP := build/Waydict.app
MACOS_CONTENTS := $(MACOS_APP)/Contents
MACOS_LDFLAGS := -s -w -X waydict/internal/buildinfo.Version=$(VERSION) -X waydict/internal/buildinfo.Commit=$(COMMIT) -X waydict/internal/buildinfo.BuildNumber=$(BUILD_NUMBER) -X waydict/internal/buildinfo.BuildTags=$(MACOS_BUILD_TAGS_METADATA)
MACOS_DMG := dist/Waydict-$(VERSION)-universal.dmg

.PHONY: build build-linux test test-linux-native test-macos-native build-macos-dev build-macos-arm64 build-macos-amd64 app-macos package-macos sign-macos notarize-macos verify-macos smoke-macos release-inputs test-macos-bundle test-sherpa test-pipewire test-model test-whisper install clean

build:
	$(GO_ENV) $(GO) build -tags "$(BUILD_TAGS)" -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/waydict

build-linux:
	@echo "linux toolchain: $$($(GO) version); native pins: $$(tr '\n' ' ' < third_party/versions.lock)"
	GOOS=linux CGO_ENABLED=0 $(GO) build ./...

test:
	@echo "test toolchain: $$($(GO) version); native pins: $$(tr '\n' ' ' < third_party/versions.lock)"
	$(GO) test ./...

test-linux-native:
	@echo "linux native toolchain: $$($(GO) version); native pins: $$(tr '\n' ' ' < third_party/versions.lock)"
	GOOS=linux CGO_ENABLED=0 $(GO) test ./...
	GOOS=linux CGO_ENABLED=0 $(GO) vet ./...

test-macos-native: release-inputs
	$(GO) test ./...
	$(GO) test -race ./...
	$(GO) test ./packaging/macos

release-inputs:
	@VERSION='$(VERSION)' BUILD_NUMBER='$(BUILD_NUMBER)' RELEASE='$(RELEASE)' bash scripts/macos/release-info.sh

build-macos-dev: release-inputs
	bash scripts/macos/build-whisper.sh $(MACOS_ARCH)
	bash scripts/macos/build-onnxruntime.sh $(MACOS_ARCH)
	$(RM) -r $(MACOS_APP)
	install -d $(MACOS_CONTENTS)/MacOS $(MACOS_CONTENTS)/Resources/en.lproj $(MACOS_CONTENTS)/Frameworks
	$(MACOS_GO_ENV) $(GO) build -tags "$(BUILD_TAGS_MACOS)" -trimpath -ldflags "$(MACOS_LDFLAGS)" -o $(MACOS_CONTENTS)/MacOS/waydict-app ./cmd/waydict-app
	$(MACOS_GO_ENV) $(GO) build -tags "$(BUILD_TAGS_MACOS)" -trimpath -ldflags "$(MACOS_LDFLAGS)" -o $(MACOS_CONTENTS)/MacOS/waydict ./cmd/waydict
	bash scripts/macos/package-sherpa.sh $(MACOS_APP) $(MACOS_ARCH)
	sed -e 's|@VERSION@|$(VERSION)|g' -e 's|@BUILD_NUMBER@|$(BUILD_NUMBER)|g' packaging/macos/Info.plist.in > $(MACOS_CONTENTS)/Info.plist
	cp packaging/macos/Resources/en.lproj/Localizable.strings packaging/macos/Resources/en.lproj/InfoPlist.strings $(MACOS_CONTENTS)/Resources/en.lproj/
	cp packaging/macos/Resources/model-catalog.json $(MACOS_CONTENTS)/Resources/
	cp packaging/macos/README.txt packaging/macos/THIRD_PARTY_NOTICES.md LICENSE $(MACOS_CONTENTS)/Resources/
	$(GO) run ./scripts/macos/icon -output $(MACOS_CONTENTS)/Resources/Waydict.icns
	printf 'APPL????' > $(MACOS_CONTENTS)/PkgInfo
	plutil -lint $(MACOS_CONTENTS)/Info.plist
	plutil -lint $(MACOS_CONTENTS)/Resources/en.lproj/Localizable.strings $(MACOS_CONTENTS)/Resources/en.lproj/InfoPlist.strings
	# Development bundles remain ad-hoc signed without hardened runtime.
	for dylib in $(MACOS_CONTENTS)/Frameworks/*.dylib; do codesign -s - --force $$dylib; done
	codesign -s - --force --entitlements packaging/macos/Waydict.entitlements $(MACOS_CONTENTS)/MacOS/waydict
	codesign -s - --force --entitlements packaging/macos/Waydict.entitlements $(MACOS_CONTENTS)/MacOS/waydict-app
	codesign -s - --force --entitlements packaging/macos/Waydict.entitlements $(MACOS_APP)

build-macos-arm64: release-inputs
	bash scripts/macos/build-app-arch.sh arm64 '$(VERSION)' '$(BUILD_NUMBER)' '$(COMMIT)'

build-macos-amd64: release-inputs
	bash scripts/macos/build-app-arch.sh x86_64 '$(VERSION)' '$(BUILD_NUMBER)' '$(COMMIT)'

app-macos: build-macos-arm64 build-macos-amd64
	bash scripts/macos/make-universal-app.sh

package-macos: app-macos
	bash scripts/macos/make-dmg.sh $(MACOS_APP) '$(VERSION)' $(MACOS_DMG)
	codesign --force --sign - $(MACOS_DMG)
	bash scripts/macos/release-artifacts.sh '$(VERSION)' $(MACOS_DMG)

sign-macos: release-inputs
	bash scripts/macos/sign.sh $(MACOS_APP) '$(DEVELOPER_ID_APPLICATION)'

notarize-macos: release-inputs
	bash scripts/macos/notarize.sh $(MACOS_APP) '$(NOTARY_PROFILE)' '$(VERSION)'
	bash scripts/macos/release-artifacts.sh '$(VERSION)' $(MACOS_DMG)

verify-macos: release-inputs
	bash scripts/macos/verify.sh '$(ARTIFACT)'

smoke-macos: release-inputs
	bash scripts/macos/smoke.sh '$(ARTIFACT)'

test-macos-bundle:
	$(GO) test ./packaging/macos

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
	$(RM) -r build/Waydict.app build/macos build/dmg-root dist
