{
  description = "waydict — local voice dictation (wlroots Wayland: PipeWire + sherpa-onnx + wtype; macOS: CoreAudio + Quartz)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      linuxSystems = [ "x86_64-linux" "aarch64-linux" ];
      darwinSystems = [ "aarch64-darwin" "x86_64-darwin" ];
      forSystems = systems: f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      whisperCompat = pkgs: pkgs.whisper-cpp-vulkan.overrideAttrs (old: {
        # The cgo integration links backends directly and does not call ggml_backend_load_all.
        cmakeFlags = old.cmakeFlags ++ [
          "-DGGML_BACKEND_DL:BOOL=OFF"
          "-DGGML_CPU_ALL_VARIANTS:BOOL=OFF"
          "-DGGML_BACKEND_DIR:STRING="
        ];
        postFixup = (old.postFixup or "") + ''
          sed -i "s|^libdir=.*|libdir=$out/lib|" "$out/lib/pkgconfig/whisper.pc"
        '';
      });
      # Linux dev shell (PipeWire capture + Vulkan Whisper).
      linuxDevShell = pkgs: { withWhisper ? true }: pkgs.mkShell {
        nativeBuildInputs = [ pkgs.pkg-config ];
        buildInputs = [ pkgs.pipewire pkgs.go ]
          ++ pkgs.lib.optionals withWhisper [ (whisperCompat pkgs) pkgs.vulkan-loader ];
        CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
        LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib ];
      };
      # macOS dev shell. Native app/ASR frameworks (AppKit, AVFoundation,
      # CoreAudio, Metal, Accelerate) and the code-signing/notarization tools
      # come from Xcode; the whisper.cpp submodule is built via
      # scripts/macos/build-whisper.sh, not from nixpkgs. Nix supplies only the
      # Go toolchain plus CMake/Ninja/pkg-config so cgo uses Xcode's clang/SDK.
      darwinDevShell = pkgs: pkgs.mkShellNoCC {
        packages = [ pkgs.go pkgs.pkg-config pkgs.cmake pkgs.ninja ];
        CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
        shellHook = ''
          export CGO_ENABLED=1
          export CC=/usr/bin/clang
          export CXX=/usr/bin/clang++
          export MACOSX_DEPLOYMENT_TARGET=13.0
        '';
      };
      waydictPackage = pkgs: pkgs.lib.makeOverridable
        ({ withWhisper ? true }: pkgs.buildGoModule {
          pname = "waydict";
          version = "0.1.0";
          src = self;

          # go mod vendor strips the prebuilt sherpa .so (non-Go files); use the
          # module cache instead so the cgo link can find them.
          proxyVendor = true;
          vendorHash = "sha256-9JchK62+xVwSOfXQp3yOQRKmztNABuLYqMXbw2VIAXc=";

          tags = [ "sherpa" "pipewire" ] ++ pkgs.lib.optional withWhisper "whispercpp";

          # The cgo test binaries need sherpa/libstdc++ on the runtime linker
          # path; tests are run in the dev shell / CI, not in the package build.
          doCheck = false;

          nativeBuildInputs = [ pkgs.pkg-config pkgs.autoPatchelfHook ];
          # pipewire: cgo pkg-config dep. stdenv.cc.cc.lib: libstdc++ for the
          # prebuilt sherpa/onnxruntime .so that autoPatchelfHook relocates.
          buildInputs = [ pkgs.pipewire pkgs.stdenv.cc.cc.lib ]
            ++ pkgs.lib.optionals withWhisper [ (whisperCompat pkgs) pkgs.vulkan-loader ];

          env.CGO_ENABLED = "1";
          env.CGO_CFLAGS_ALLOW = "-fno-strict-overflow";

          # The sherpa-onnx-go module ships prebuilt .so files and links the
          # binary with an rpath into the (GC-able) Go module cache. Copy them
          # into the package and point the binary at $out/lib instead.
          postInstall = ''
            mkdir -p $out/lib
            cp -v "$GOPATH"/pkg/mod/github.com/k2-fsa/sherpa-onnx-go-linux@*/lib/${pkgs.stdenv.hostPlatform.config}/lib*.so $out/lib/
            chmod u+w $out/lib/*.so
            patchelf --set-rpath $out/lib $out/bin/waydict
          '';

          meta = with nixpkgs.lib; {
            description = "Local voice dictation for wlroots Wayland compositors";
            homepage = "https://github.com/zhangja233/waydict";
            license = licenses.gpl3Only;
            mainProgram = "waydict";
            platforms = linuxSystems;
          };
        })
        { };
    in
    {
      # The nix-built package is Linux-only; the macOS app is produced by the
      # Xcode-based `make` targets in Section 21 of the port spec.
      packages = forSystems linuxSystems (pkgs: rec {
        default = waydictPackage pkgs;
        sherpa = default.override { withWhisper = false; };
      });

      devShells =
        (forSystems linuxSystems (pkgs: {
          default = linuxDevShell pkgs { };
          sherpa = linuxDevShell pkgs { withWhisper = false; };
        }))
        // (forSystems darwinSystems (pkgs: {
          default = darwinDevShell pkgs;
        }));
    };
}
