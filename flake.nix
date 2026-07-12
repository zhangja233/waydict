{
  description = "waydict — local voice dictation for wlroots Wayland (PipeWire + sherpa-onnx + wtype)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
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
    in
    {
      packages = forAll (pkgs: {
        default = pkgs.buildGoModule {
          pname = "waydict";
          version = "0.1.0";
          src = self;

          # go mod vendor strips the prebuilt sherpa .so (non-Go files); use the
          # module cache instead so the cgo link can find them.
          proxyVendor = true;
          vendorHash = "sha256-mbvsfsuwCrW2TaVmBF1GZ6UfXgZvMfGpgYFa2I3G8Ck=";

          tags = [ "sherpa" "pipewire" "whispercpp" ];

          # The cgo test binaries need sherpa/libstdc++ on the runtime linker
          # path; tests are run in the dev shell / CI, not in the package build.
          doCheck = false;

          nativeBuildInputs = [ pkgs.pkg-config pkgs.autoPatchelfHook ];
          # pipewire: cgo pkg-config dep. stdenv.cc.cc.lib: libstdc++ for the
          # prebuilt sherpa/onnxruntime .so that autoPatchelfHook relocates.
          buildInputs = [ pkgs.pipewire pkgs.stdenv.cc.cc.lib (whisperCompat pkgs) pkgs.vulkan-loader ];

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
            platforms = systems;
          };
        };
      });

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          nativeBuildInputs = [ pkgs.pkg-config ];
          buildInputs = [ pkgs.pipewire pkgs.go (whisperCompat pkgs) pkgs.vulkan-loader ];
          CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
          LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib ];
        };
      });
    };
}
