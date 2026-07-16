# Third-party notices

Dependency licenses and notices are distributed with the source repository and release artifacts. Speech models are installed separately under their upstream licenses.

Waydict statically links whisper.cpp v1.9.1 and its bundled GGML implementation, distributed under the MIT License. See `third_party/whisper.cpp/LICENSE` in the source distribution.

Waydict bundles sherpa-onnx v1.13.3 and the sherpa-onnx C API, distributed under the Apache License 2.0. Source and license: <https://github.com/k2-fsa/sherpa-onnx>.

Waydict bundles ONNX Runtime v1.24.4, distributed under the MIT License. Source and license: <https://github.com/microsoft/onnxruntime>.

Waydict links github.com/rivo/uniseg v0.4.7, distributed under the MIT License. Source and license: <https://github.com/rivo/uniseg>.

All other Go module dependencies, versions, and declared license conclusions are listed in the SPDX SBOM shipped beside each release artifact.
