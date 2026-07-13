package inject

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"waydict/internal/config"
)

var spaces = regexp.MustCompile(`[ \t\f\v]+`)

type CaseState struct {
	AtBoundary bool
}

type PostProcessor struct {
	cfg                config.PostProcess
	appendSpace        bool
	replacements       *regexp.Regexp
	replacementTargets map[string]string
	protect            map[string]struct{}
	standaloneI        *regexp.Regexp
}

func NewPostProcessor(cfg config.PostProcess, appendSpace bool, vocabulary []string) PostProcessor {
	type pair struct {
		from string
		to   string
	}
	pairs := make([]pair, 0, len(cfg.Replacements))
	for from, to := range cfg.Replacements {
		pairs = append(pairs, pair{from: from, to: to})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if len(pairs[i].from) != len(pairs[j].from) {
			return len(pairs[i].from) > len(pairs[j].from)
		}
		return pairs[i].from < pairs[j].from
	})
	alternatives := make([]string, 0, len(pairs))
	targets := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		alternatives = append(alternatives, regexp.QuoteMeta(pair.from))
		key := strings.ToLower(pair.from)
		if _, exists := targets[key]; !exists {
			targets[key] = pair.to
		}
	}
	var replacements *regexp.Regexp
	if len(alternatives) > 0 {
		replacements = regexp.MustCompile(`(?i)\b(` + strings.Join(alternatives, "|") + `)\b`)
	}
	protect := make(map[string]struct{}, len(vocabulary)+len(targets))
	for _, word := range vocabulary {
		protect[strings.ToLower(word)] = struct{}{}
	}
	// Protect each replacement target's first word so smart casing does not
	// undo the casing the replacement just applied (e.g. cloud→Claude).
	for _, to := range targets {
		if fields := strings.Fields(to); len(fields) > 0 {
			protect[strings.ToLower(fields[0])] = struct{}{}
		}
	}
	return PostProcessor{
		cfg:                cfg,
		appendSpace:        appendSpace,
		replacements:       replacements,
		replacementTargets: targets,
		protect:            protect,
		standaloneI:        regexp.MustCompile(`\bi\b`),
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
	if p.replacements != nil {
		text = p.replacements.ReplaceAllStringFunc(text, func(match string) string {
			if target, ok := p.replacementTargets[strings.ToLower(match)]; ok {
				return target
			}
			return match
		})
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
	if _, ok := p.protect[strings.ToLower(base)]; ok {
		return true
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
