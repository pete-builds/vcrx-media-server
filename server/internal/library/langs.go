package library

import "strings"

// LanguageCode turns any language tag a file or a provider uses ("en",
// "eng", "English", "pt-br") into the ISO 639-2/B code tracks are stored with,
// or "" when it is not a language.
func LanguageCode(tag string) string { return languageCode(tag) }

// iso6391 maps a stored ISO 639-2/B code back to its two-letter ISO 639-1 code,
// built from the same table as LanguageCode.
var iso6391 = func() map[string]string {
	m := map[string]string{}
	for alias, code := range langAliases {
		if len(alias) == 2 {
			if prev, ok := m[code]; !ok || alias < prev {
				m[code] = alias
			}
		}
	}
	// Two-letter aliases that are not the ISO 639-1 code of their language.
	m["nor"], m["heb"] = "no", "he"
	m["hin"] = "hi"
	return m
}()

// ISO6391 returns the two-letter code for a language tag, which is what
// OpenSubtitles searches by, or "" when there is none.
func ISO6391(tag string) string {
	return iso6391[languageCode(strings.ToLower(strings.TrimSpace(tag)))]
}
