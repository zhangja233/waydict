package inject

import (
	"regexp"
	"strings"
	"unicode"

	"waydict/internal/config"
)

var spaces = regexp.MustCompile(`[ \t\f\v]+`)

type CaseState struct {
	AtBoundary bool
}

type PostProcessor struct {
	cfg         config.PostProcess
	appendSpace bool
	standaloneI *regexp.Regexp
}

func NewPostProcessor(cfg config.PostProcess, appendSpace bool) PostProcessor {
	return PostProcessor{
		cfg:         cfg,
		appendSpace: appendSpace,
		standaloneI: regexp.MustCompile(`\bi\b`),
	}
}

func (p PostProcessor) Apply(text string, st CaseState) (out string, next CaseState) {
	if p.cfg.TrimLeading {
		text = strings.TrimLeft(text, " \t\r\n")
	}
	if strings.TrimSpace(text) == "" {
		return "", st
	}
	if p.cfg.SpokenFormattingCommands {
		if out, ok := spokenCommand(text); ok {
			switch out {
			case "\n", "\n\n":
				return out, CaseState{AtBoundary: true}
			default:
				return out, st
			}
		}
	}
	if p.cfg.CollapseSpaces {
		text = spaces.ReplaceAllString(text, " ")
	}
	if p.cfg.FixPunctuationSpacing {
		text = fixPunctuationSpacing(text)
	}
	next = st
	if p.cfg.SmartCase {
		complete := sentenceComplete(text)
		text = p.standaloneI.ReplaceAllString(text, "I")
		text = p.caseFirstWord(text, st.AtBoundary && complete)
		next = CaseState{AtBoundary: complete}
	}
	if p.appendSpace && !strings.HasSuffix(text, "\n") && !strings.HasSuffix(text, "\t") {
		text += " "
	}
	return text, next
}

func sentenceComplete(text string) bool {
	text = strings.TrimRightFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\'' || unicode.Is(unicode.Pe, r) || unicode.Is(unicode.Pf, r)
	})
	return strings.HasSuffix(text, ".") || strings.HasSuffix(text, "?") || strings.HasSuffix(text, "!")
}

func (p PostProcessor) caseFirstWord(text string, capitalize bool) string {
	runes := []rune(text)
	start := -1
	for i, r := range runes {
		if unicode.IsLetter(r) {
			if i > 0 && (unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				return text
			}
			start = i
			break
		}
	}
	if start < 0 {
		return text
	}
	end := start
	for end < len(runes) && (unicode.IsLetter(runes[end]) || runes[end] == '\'' || runes[end] == '’') {
		end++
	}
	word := string(runes[start:end])
	if p.preserveFirstWord(word) {
		return text
	}
	if capitalize {
		runes[start] = unicode.ToUpper(runes[start])
	} else {
		runes[start] = unicode.ToLower(runes[start])
	}
	return string(runes)
}

func (p PostProcessor) preserveFirstWord(word string) bool {
	base := word
	if strings.HasSuffix(base, "'s") {
		base = strings.TrimSuffix(base, "'s")
	} else if strings.HasSuffix(base, "’s") {
		base = strings.TrimSuffix(base, "’s")
	}
	if word == "I" || strings.HasPrefix(word, "I'") || strings.HasPrefix(word, "I’") {
		return true
	}
	letters := 0
	for _, r := range base {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return letters >= 2
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
