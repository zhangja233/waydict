{ pkgs ? import <nixpkgs> { }, withWhisper ? true, whisperBackend ? "vulkan" }:

let
  # libwhisper carrying the ggml backend a given asr.provider needs; the binary links
  # one backend, so the provider is a build-time choice. CUDA requires an
  # unfree-permitting nixpkgs, re-imported here because the caller's may not allow it.
  whisperCompat =
    let
      base =
        if whisperBackend == "cuda" then
          (import <nixpkgs> {
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
      # Linking the CUDA backend in (GGML_BACKEND_DL=OFF) makes every binary need
      # libcuda.so.1 at load time; it ships with the driver, not the sandbox, so
      # nixpkgs' `whisper-cli --help` install check cannot pass.
      doInstallCheck = if whisperBackend == "cuda" then false else (old.doInstallCheck or true);
    });
in
pkgs.mkShell {
  nativeBuildInputs = [ pkgs.pkg-config ];
  buildInputs = [ pkgs.pipewire ]
    ++ pkgs.lib.optionals withWhisper
      ([ whisperCompat ] ++ pkgs.lib.optional (whisperBackend != "cuda") pkgs.vulkan-loader);

  CGO_CFLAGS_ALLOW = "-fno-strict-overflow";
  # libcuda.so ships with the host driver, not the store.
  LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath [ pkgs.stdenv.cc.cc.lib ]
    + pkgs.lib.optionalString (whisperBackend == "cuda") ":/run/opengl-driver/lib";
}
