package inject

import (
	"regexp"
	"strings"

	"waydict/internal/config"
)

var spaces = regexp.MustCompile(`[ \t\f\v]+`)

type PostProcessor struct {
	cfg         config.PostProcess
	appendSpace bool
}

func NewPostProcessor(cfg config.PostProcess, appendSpace bool) PostProcessor {
	return PostProcessor{cfg: cfg, appendSpace: appendSpace}
}

func (p PostProcessor) Apply(text string) string {
	if p.cfg.TrimLeading {
		text = strings.TrimLeft(text, " \t\r\n")
	}
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if p.cfg.SpokenFormattingCommands {
		if out, ok := spokenCommand(text); ok {
			return out
		}
	}
	if p.cfg.CollapseSpaces {
		text = spaces.ReplaceAllString(text, " ")
	}
	if p.cfg.FixPunctuationSpacing {
		text = fixPunctuationSpacing(text)
	}
	if p.appendSpace && !strings.HasSuffix(text, "\n") && !strings.HasSuffix(text, "\t") {
		text += " "
	}
	return text
}

func spokenCommand(text string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(text))
	switch n {
	case "new line":
		return "\n", true
	case "new paragraph":
		return "\n\n", true
	case "tab":
		return "\t", true
	case "scratch that":
		return "", true
	default:
		return "", false
	}
}

func fixPunctuationSpacing(text string) string {
	var b strings.Builder
	var prev rune
	for _, r := range text {
		if strings.ContainsRune(".,?!:;)]}", r) && prev == ' ' {
			s := b.String()
			b.Reset()
			b.WriteString(strings.TrimRight(s, " "))
		}
		if prev != 0 && strings.ContainsRune("([{", prev) && r == ' ' {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}
