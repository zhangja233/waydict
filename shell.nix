{ pkgs ? import <nixpkgs> { } }:

let
  whisperCompat = pkgs.whisper-cpp-vulkan.overrideAttrs (old: {
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
pkgs.mkShell {
  nativeBuildInputs = [ pkgs.pkg-config ];
  buildInputs = [ pkgs.pipewire whisperCompat pkgs.vulkan-loader ];

  CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
  LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib ];
}
