package model

import "testing"

func TestWhisperAssetForNameReturnsCatalogAsset(t *testing.T) {
	asset, err := WhisperAssetForName(WhisperSmallEnModel)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Model != WhisperSmallEnModel || asset.File != "ggml-small.en.bin" || asset.Size != 487614201 || asset.SHA256 == "" {
		t.Fatalf("catalog asset = %+v", asset)
	}
}

func TestWhisperAssetForNameSynthesizesUnknownAsset(t *testing.T) {
	asset, err := WhisperAssetForName("ggml-base.en")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Model != "ggml-base.en" || asset.File != "ggml-base.en.bin" || asset.URL != "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin" || asset.Size != 0 || asset.SHA256 != "" {
		t.Fatalf("synthesized asset = %+v", asset)
	}
}

func TestWhisperAssetForNameRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", "../x"} {
		if _, err := WhisperAssetForName(name); err == nil {
			t.Fatalf("WhisperAssetForName(%q) unexpectedly succeeded", name)
		}
	}
}
