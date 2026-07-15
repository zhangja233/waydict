package quartz

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

const maxUTF16Units = 20

type chunkKind uint8

const (
	chunkUnicode chunkKind = iota
	chunkReturn
	chunkTab
)

type textChunk struct {
	kind chunkKind
	text string
}

func chunkText(text string) ([]textChunk, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("text is not valid UTF-8")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return nil, nil
	}

	var (
		chunks []textChunk
		buffer strings.Builder
		units  int
	)
	flush := func() {
		if buffer.Len() == 0 {
			return
		}
		chunks = append(chunks, textChunk{kind: chunkUnicode, text: buffer.String()})
		buffer.Reset()
		units = 0
	}

	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		cluster := graphemes.Str()
		switch cluster {
		case "\n":
			flush()
			chunks = append(chunks, textChunk{kind: chunkReturn})
			continue
		case "\t":
			flush()
			chunks = append(chunks, textChunk{kind: chunkTab})
			continue
		}
		clusterUnits := len(utf16.Encode([]rune(cluster)))
		if clusterUnits > maxUTF16Units {
			return nil, fmt.Errorf("grapheme cluster exceeds %d UTF-16 code units", maxUTF16Units)
		}
		if units+clusterUnits > maxUTF16Units {
			flush()
		}
		buffer.WriteString(cluster)
		units += clusterUnits
	}
	flush()
	return chunks, nil
}
