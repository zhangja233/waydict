package quartz

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestChunkTextASCII(t *testing.T) {
	chunks := mustChunk(t, strings.Repeat("a", 25))
	assertUnicodeChunks(t, chunks, strings.Repeat("a", 20), strings.Repeat("a", 5))
}

func TestChunkTextNonASCII(t *testing.T) {
	chunks := mustChunk(t, strings.Repeat("界", 21))
	assertUnicodeChunks(t, chunks, strings.Repeat("界", 20), "界")
}

func TestChunkTextKeepsZWJEmojiTogether(t *testing.T) {
	family := "👨‍👩‍👧‍👦"
	chunks := mustChunk(t, strings.Repeat("a", 10)+family+"b")
	assertUnicodeChunks(t, chunks, strings.Repeat("a", 10), family+"b")
}

func TestChunkTextKeepsCombiningMarksTogether(t *testing.T) {
	combined := "e\u0301"
	chunks := mustChunk(t, strings.Repeat("a", 19)+combined)
	assertUnicodeChunks(t, chunks, strings.Repeat("a", 19), combined)
}

func TestChunkTextSurrogatePairBoundaries(t *testing.T) {
	t.Run("fits at boundary", func(t *testing.T) {
		chunks := mustChunk(t, strings.Repeat("a", 18)+"😀")
		assertUnicodeChunks(t, chunks, strings.Repeat("a", 18)+"😀")
	})
	t.Run("moves whole pair", func(t *testing.T) {
		chunks := mustChunk(t, strings.Repeat("a", 19)+"😀")
		assertUnicodeChunks(t, chunks, strings.Repeat("a", 19), "😀")
	})
}

func TestChunkTextNormalizesAndFlushesControls(t *testing.T) {
	chunks := mustChunk(t, "a\r\nb\rc\td")
	want := []textChunk{
		{kind: chunkUnicode, text: "a"},
		{kind: chunkReturn},
		{kind: chunkUnicode, text: "b"},
		{kind: chunkReturn},
		{kind: chunkUnicode, text: "c"},
		{kind: chunkTab},
		{kind: chunkUnicode, text: "d"},
	}
	if len(chunks) != len(want) {
		t.Fatalf("chunk count = %d, want %d", len(chunks), len(want))
	}
	for index := range want {
		if chunks[index] != want[index] {
			t.Fatalf("chunk %d = %#v, want %#v", index, chunks[index], want[index])
		}
	}
}

func mustChunk(t *testing.T, text string) []textChunk {
	t.Helper()
	chunks, err := chunkText(text)
	if err != nil {
		t.Fatalf("chunkText: %v", err)
	}
	for index, chunk := range chunks {
		if chunk.kind != chunkUnicode {
			continue
		}
		if units := len(utf16.Encode([]rune(chunk.text))); units > maxUTF16Units {
			t.Fatalf("chunk %d has %d UTF-16 units", index, units)
		}
	}
	return chunks
}

func assertUnicodeChunks(t *testing.T, got []textChunk, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("chunk count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].kind != chunkUnicode || got[index].text != want[index] {
			t.Fatalf("chunk %d = %#v, want %q", index, got[index], want[index])
		}
	}
}
