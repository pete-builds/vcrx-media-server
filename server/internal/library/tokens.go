package library

import (
	"strings"
	"unicode"
)

// token is one word of a release name. Dots and underscores are separators;
// hyphens stay inside a word unless one side of it is a tag or a year, so
// "Rope-Walker" survives while "Quillbeast-720p" splits.
type token struct {
	text  string
	lower string
	group int  // 0 outside brackets, else a number shared by one (...) or [...] group
	brack byte // '(' or '[' for grouped tokens
}

// tokenize splits a file or folder name, without its extension.
func tokenize(name string) []token {
	s := separateDots(strings.ReplaceAll(name, "_", " "))
	var out []token
	var cur strings.Builder
	group, groupSeq := 0, 0
	var brack byte
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		for _, w := range splitHyphens(cur.String()) {
			out = append(out, token{text: w, lower: strings.ToLower(w), group: group, brack: brack})
		}
		cur.Reset()
	}
	for _, r := range s {
		switch r {
		case '(', '[', '{':
			flush()
			groupSeq++
			group = groupSeq
			brack = '('
			if r == '[' {
				brack = '['
			}
		case ')', ']', '}':
			flush()
			group, brack = 0, 0
		case ' ', '\t', ';', '+':
			// '+' only separates in names like "DD+ 5.1"; inside tags it is kept below.
			if r == '+' && cur.Len() > 0 {
				cur.WriteRune(r)
				continue
			}
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return cleanTokens(out)
}

// separateDots turns dots into spaces, but keeps them in numbers (5.1, H.264),
// in letter acronyms (L.A., S.W.A.T.) and as an abbreviation stop (Mr. Smith).
func separateDots(s string) string {
	rs := []rune(s)
	// keep: 1 inside an acronym, 2 on its final dot.
	keep := make([]byte, len(rs))
	isSep := func(i int) bool { return i < 0 || i >= len(rs) || strings.ContainsRune(" .-([", rs[i]) }
	for i := 0; i < len(rs); i++ {
		if !unicode.IsLetter(rs[i]) || !isSep(i-1) {
			continue
		}
		j, n := i, 0
		for j+1 < len(rs) && unicode.IsLetter(rs[j]) && rs[j+1] == '.' {
			n++
			j += 2
		}
		if n >= 2 {
			for k := i + 1; k < j; k += 2 {
				keep[k] = 1
			}
			keep[j-1] = 2
			i = j - 1
		}
	}
	var b strings.Builder
	for i, r := range rs {
		if r != '.' {
			b.WriteRune(r)
			continue
		}
		nextDigit := i+1 < len(rs) && unicode.IsDigit(rs[i+1])
		switch {
		case keep[i] == 1:
			b.WriteRune('.')
		case keep[i] == 2:
			b.WriteRune('.')
			if i+1 < len(rs) && rs[i+1] != ' ' {
				b.WriteRune(' ')
			}
		case channelDot(rs, i):
			b.WriteRune('.')
		case nextDigit && i > 0 && (rs[i-1] == 'H' || rs[i-1] == 'h') && isSep(i-2):
			b.WriteRune('.')
		case i > 0 && unicode.IsLetter(rs[i-1]) && i+1 < len(rs) && rs[i+1] == ' ':
			b.WriteRune('.')
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// channelDot reports whether the dot at i sits in a channel layout such as
// 5.1 or AAC2.0: at most two digits before it and exactly one after, so that
// "1968.1080p" still splits.
func channelDot(rs []rune, i int) bool {
	before := 0
	for j := i - 1; j >= 0 && unicode.IsDigit(rs[j]); j-- {
		before++
	}
	return before >= 1 && before <= 2 && i+1 < len(rs) && unicode.IsDigit(rs[i+1]) &&
		(i+2 >= len(rs) || !unicode.IsDigit(rs[i+2]))
}

// splitHyphens breaks a hyphenated word apart when any part is a tag or a year,
// unless the whole word is itself a tag (WEB-DL, DTS-HD, Blu-Ray).
func splitHyphens(w string) []string {
	if !strings.Contains(w, "-") {
		return []string{w}
	}
	if classifyWord(strings.ToLower(w)).kind != tagNone {
		return []string{w}
	}
	parts := strings.Split(w, "-")
	split := false
	for _, p := range parts {
		lp := strings.ToLower(p)
		if p != "" && (classifyWord(lp).kind != tagNone || isYearWord(lp)) {
			split = true
		}
	}
	if !split {
		return []string{w}
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cleanTokens drops punctuation-only tokens and trims stray hyphens.
func cleanTokens(in []token) []token {
	out := in[:0]
	for _, t := range in {
		// Title punctuation such as "Harrowgate!" or "Directive:" stays; scene
		// decoration such as "~FX~" goes.
		raw := t.text
		t.text = strings.Trim(t.text, "-~=*#\"")
		if isDashText(raw) || raw == "&" {
			// A spaced hyphen or ampersand is title punctuation:
			// "Pellingham - Origins", "Salt & Quills".
			t.text, t.lower = raw, raw
			out = append(out, t)
			continue
		}
		if t.text == "" || !hasAlnum(t.text) {
			continue
		}
		t.lower = strings.ToLower(t.text)
		out = append(out, t)
	}
	return out
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func isYearWord(s string) bool {
	if len(s) != 4 || (s[:2] != "19" && s[:2] != "20") {
		return false
	}
	for i := range 4 {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isDashText matches a hyphen, en dash (U+2013) or em dash (U+2014) standing
// alone between words.
func isDashText(s string) bool { return s == "-" || s == "\u2013" || s == "\u2014" }

func isDash(t token) bool { return isDashText(t.text) }

// trimDashes drops spaced hyphens at either end of a token run.
func trimDashes(ts []token) []token {
	for len(ts) > 0 && isDash(ts[0]) {
		ts = ts[1:]
	}
	for len(ts) > 0 && isDash(ts[len(ts)-1]) {
		ts = ts[:len(ts)-1]
	}
	return ts
}

// joinTitle renders tokens back into a title, restoring bracket groups.
func joinTitle(ts []token) string {
	ts = trimDashes(ts)
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			b.WriteByte(' ')
		}
		open := t.group != 0 && (i == 0 || ts[i-1].group != t.group)
		closing := t.group != 0 && (i == len(ts)-1 || ts[i+1].group != t.group)
		if open {
			b.WriteByte(t.brack)
		}
		b.WriteString(t.text)
		if closing {
			if t.brack == '[' {
				b.WriteByte(']')
			} else {
				b.WriteByte(')')
			}
		}
	}
	return fixCase(b.String())
}

var smallWords = map[string]bool{"a": true, "an": true, "the": true, "of": true, "and": true, "or": true, "in": true,
	"on": true, "at": true, "to": true, "for": true, "with": true, "by": true, "from": true, "but": true, "vs": true}

// fixCase title-cases names written in capitals ("QUORVEX HARBOR NIGHTS"),
// leaving short acronyms such as "QRX" or "QRV" alone.
func fixCase(s string) string {
	letters, upper := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	words := strings.Fields(s)
	if letters == 0 || upper != letters || (len(words) == 1 && letters <= 4) {
		return s
	}
	for i, w := range words {
		lw := strings.ToLower(w)
		switch {
		case isRoman(w):
		case strings.Contains(w, ".") && len(w) <= 8:
			// Acronyms with dots stay as written.
		case i > 0 && smallWords[lw]:
			words[i] = lw
		default:
			rs := []rune(lw)
			for j, r := range rs {
				if unicode.IsLetter(r) {
					rs[j] = unicode.ToUpper(r)
					break
				}
			}
			words[i] = string(rs)
		}
	}
	return strings.Join(words, " ")
}

func isRoman(w string) bool {
	if len(w) < 2 || len(w) > 5 {
		return false
	}
	for _, r := range w {
		if !strings.ContainsRune("IVX", r) {
			return false
		}
	}
	return true
}

var foldReplacer = strings.NewReplacer(
	"&", " and ", "'", "", "’", "", "`", "",
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "æ", "ae",
	"ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ñ", "n",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o", "œ", "oe",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y", "ß", "ss",
)

// NormalizeTitle folds a title to the form used in grouping keys: lower case,
// accents and apostrophes dropped, "&" as "and", other punctuation as spaces.
func NormalizeTitle(s string) string {
	s = foldReplacer.Replace(strings.ToLower(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	return b.String()
}

// SortTitle moves a leading English article to the end: "The Quillery" sorts as
// "Quillery, The".
func SortTitle(title string) string {
	t := strings.TrimSpace(title)
	for _, a := range []string{"The ", "A ", "An "} {
		if len(t) > len(a) && strings.EqualFold(t[:len(a)], a) {
			return strings.TrimSpace(t[len(a):]) + ", " + t[:len(a)-1]
		}
	}
	return t
}
