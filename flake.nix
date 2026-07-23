{
  description = "waydict — local voice dictation (wlroots Wayland: PipeWire + sherpa-onnx + wtype; macOS: CoreAudio + Quartz)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      linuxSystems = [ "x86_64-linux" "aarch64-linux" ];
      darwinSystems = [ "aarch64-darwin" "x86_64-darwin" ];
      forSystems = systems: f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      # libwhisper carrying the ggml backend a given asr.provider needs. The binary links
      # one backend, so "which provider" is a build-time choice, not a runtime flag.
      #
      # CUDA needs an unfree-permitting nixpkgs — legacyPackages carries no config — so it
      # is imported lazily here. The default vulkan path never forces that import and stays
      # free and binary-cached; the cuda path compiles whisper.cpp locally.
      whisperFor = pkgs: backend:
        let
          base =
            if backend == "cuda" then
              (import nixpkgs {
                inherit (pkgs.stdenv.hostPlatform) system;
                config = {
                  allowUnfree = true;
                  cudaSupport = true;
                };
              }).whisper-cpp
            else
              pkgs.whisper-cpp-vulkan;
        in
        base.overrideAttrs (old: {
          # The cgo integration links backends directly and does not call ggml_backend_load_all.
          cmakeFlags = old.cmakeFlags ++ [
            "-DGGML_BACKEND_DL:BOOL=OFF"
            "-DGGML_CPU_ALL_VARIANTS:BOOL=OFF"
            "-DGGML_BACKEND_DIR:STRING="
          ];
          postFixup = (old.postFixup or "") + ''
            sed -i "s|^libdir=.*|libdir=$out/lib|" "$out/lib/pkgconfig/whisper.pc"
          '';
          # nixpkgs' install check runs `whisper-cli --help`, which is fine on its own
          # build because GGML_BACKEND_DL leaves the CUDA backend in a dlopened .so.
          # Linking it in above means every binary needs libcuda.so.1 at load time, and
          # that ships with the driver, not the sandbox — so the check cannot pass here.
          doInstallCheck = if backend == "cuda" then false else (old.doInstallCheck or true);
        });
      # libcuda.so is not in the build sandbox — it ships with the host driver — so a CUDA
      # build needs the driver runpath stamped in rather than resolved at link time.
      whisperRuntime = pkgs: backend:
        if backend == "cuda"
        then [ ]
        else [ pkgs.vulkan-loader ];
      # Linux dev shell (PipeWire capture + GPU Whisper).
      linuxDevShell = pkgs: { withWhisper ? true, whisperBackend ? "vulkan" }: pkgs.mkShell {
        nativeBuildInputs = [ pkgs.pkg-config ];
        buildInputs = [ pkgs.pipewire pkgs.go ]
          ++ pkgs.lib.optionals withWhisper ([ (whisperFor pkgs whisperBackend) ] ++ whisperRuntime pkgs whisperBackend);
        CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
        # The CUDA driver library lives outside the store on NixOS hosts.
        LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib ]
          + pkgs.lib.optionalString (whisperBackend == "cuda") ":/run/opengl-driver/lib";
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
        ({ withWhisper ? true, whisperBackend ? "vulkan" }: pkgs.buildGoModule {
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

          nativeBuildInputs = [ pkgs.pkg-config pkgs.autoPatchelfHook ]
            # Stamps the host driver's runpath in; without it autoPatchelfHook fails on the
            # libcuda.so.1 that libwhisper needs and the sandbox does not have.
            ++ pkgs.lib.optional (withWhisper && whisperBackend == "cuda")
              pkgs.autoAddDriverRunpath;
          # pipewire: cgo pkg-config dep. stdenv.cc.cc.lib: libstdc++ for the
          # prebuilt sherpa/onnxruntime .so that autoPatchelfHook relocates.
          buildInputs = [ pkgs.pipewire pkgs.stdenv.cc.cc.lib ]
            ++ pkgs.lib.optionals withWhisper ([ (whisperFor pkgs whisperBackend) ] ++ whisperRuntime pkgs whisperBackend);

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
        # asr.provider = "cuda". Unfree and built locally — nixpkgs does not cache CUDA
        # variants — so it is a separate output rather than the default.
        cuda = default.override { whisperBackend = "cuda"; };
      });

      devShells =
        (forSystems linuxSystems (pkgs: {
          default = linuxDevShell pkgs { };
          sherpa = linuxDevShell pkgs { withWhisper = false; };
          cuda = linuxDevShell pkgs { whisperBackend = "cuda"; };
        }))
        // (forSystems darwinSystems (pkgs: {
          default = darwinDevShell pkgs;
        }));
    };
}
