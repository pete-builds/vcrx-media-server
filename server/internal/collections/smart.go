package collections

import (
	"strings"
	"unicode"
)

// Smart collections are the Kometa-style ones an admin writes by hand: YAML
// files in the collections directory name a library and a rule over local
// metadata, and every title that matches is a member. The files are the source
// of truth; the collections rows built from them are a cache, rebuilt after
// every scan and on a timer, so losing the database loses nothing.
//
// A definition may also stamp a hidden label on every member. Access policies
// filter by label, so a rule such as "R-rated horror" can keep titles away from
// a friend without anyone labelling them one by one.

// Definition is one collection from a YAML file.
type Definition struct {
	File    string  `json:"file"`
	Line    int     `json:"line"`
	Name    string  `json:"name"`
	Library string  `json:"library"`
	Smart   Rule    `json:"smart"`
	Label   string  `json:"label,omitempty"`
	Visible Visible `json:"visible"`
}

// Visible says where a collection is listed. A collection with all three off
// exists only to stamp its label.
type Visible struct {
	// Library lists it in the library's collections and on the titles in it.
	Library bool `json:"library"`
	// Home and Shared are kept for Home rows (the admin's and other users'),
	// which do not show collections yet.
	Home   bool `json:"home"`
	Shared bool `json:"shared"`
}

// defaultVisible matches the collections table's column defaults.
var defaultVisible = Visible{Library: true, Home: false, Shared: true}

// Rule is a node of a smart filter: a group or a single condition. A group
// matches when every rule in All matches and, if Any is set, at least one rule
// in Any does.
type Rule struct {
	All  []Rule `json:"all,omitempty"`
	Any  []Rule `json:"any,omitempty"`
	Cond *Cond  `json:"cond,omitempty"`
}

// Cond is one test on a title. Values are alternatives: genre [Horror,
// Thriller] matches either.
type Cond struct {
	Field  string   `json:"field"`
	Values []string `json:"values,omitempty"`
	// Ranges are inclusive year spans, for year and decade.
	Ranges [][2]int `json:"ranges,omitempty"`
	Days   int      `json:"days,omitempty"`
	// Any is hdr: true, any HDR format.
	Any bool `json:"any,omitempty"`
}

// tagFields are the item_tags kinds a rule can test.
var tagFields = map[string]bool{
	"genre": true, "studio": true, "director": true, "writer": true,
	"actor": true, "country": true, "network": true,
}

// Fields lists every condition a rule may use, for error messages and docs.
var Fields = []string{
	"genre", "studio", "director", "writer", "actor", "country", "network",
	"decade", "year", "resolution", "hdr", "content_rating", "label", "added_within_days",
}

var hdrTypes = []string{"hdr10", "hdr10plus", "hlg", "dolby_vision"}

// slugify turns a collection name into the tail of its slug: lower case
// letters and digits, runs of anything else become one hyphen.
func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// SmartSlugPrefix starts the slug of every collection built from YAML. TMDb
// franchise collections use "tmdb-" and source 'auto'; these use source 'yaml'.
const SmartSlugPrefix = "yaml-"

// Slug is the collection's slug within its library.
func (d Definition) Slug() string { return SmartSlugPrefix + slugify(d.Name) }

// sql compiles the rule into a condition on an items alias. now is unix
// milliseconds, for added_within_days.
func (r Rule) sql(i string, now int64) (string, []any) {
	if r.Cond != nil {
		return r.Cond.sql(i, now)
	}
	var parts []string
	var args []any
	for _, sub := range r.All {
		s, a := sub.sql(i, now)
		parts = append(parts, s)
		args = append(args, a...)
	}
	if len(r.Any) > 0 {
		var alts []string
		for _, sub := range r.Any {
			s, a := sub.sql(i, now)
			alts = append(alts, s)
			args = append(args, a...)
		}
		parts = append(parts, "("+strings.Join(alts, " OR ")+")")
	}
	if len(parts) == 0 {
		// The loader refuses empty groups; never match everything by accident.
		return "0", nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", args
}

// versionsOf matches versions of the title itself (a movie) or of its episodes
// (a show), present on disk.
func versionsOf(i, cond string) string {
	return `EXISTS (SELECT 1 FROM media_versions v WHERE v.missing_since IS NULL AND ` + cond + `
		AND (v.item_id = ` + i + `.id OR v.item_id IN (SELECT e.id FROM items s JOIN items e ON e.parent_id = s.id
			WHERE s.parent_id = ` + i + `.id AND e.missing_since IS NULL)))`
}

func strArgs(vals []string) []any {
	out := make([]any, len(vals))
	for k, v := range vals {
		out[k] = v
	}
	return out
}

func (c Cond) sql(i string, now int64) (string, []any) {
	in := placeholders(len(c.Values))
	switch {
	case tagFields[c.Field]:
		// item_tags.value is COLLATE NOCASE, so IN compares without case.
		return `EXISTS (SELECT 1 FROM item_tags t WHERE t.item_id = ` + i + `.id AND t.kind = ? AND t.value IN (` + in + `))`,
			append([]any{c.Field}, strArgs(c.Values)...)
	case c.Field == "label":
		return `EXISTS (SELECT 1 FROM item_labels il JOIN labels l ON l.id = il.label_id
			WHERE il.item_id = ` + i + `.id AND l.name IN (` + in + `))`, strArgs(c.Values)
	case c.Field == "content_rating":
		up := make([]any, len(c.Values))
		for k, v := range c.Values {
			up[k] = strings.ToUpper(v)
		}
		return `UPPER(` + i + `.content_rating) IN (` + in + `)`, up
	case c.Field == "resolution":
		return versionsOf(i, `v.resolution IN (`+in+`)`), strArgs(c.Values)
	case c.Field == "hdr" && c.Any:
		return versionsOf(i, `v.hdr_type IS NOT NULL`), nil
	case c.Field == "hdr":
		return versionsOf(i, `v.hdr_type IN (`+in+`)`), strArgs(c.Values)
	case c.Field == "year" || c.Field == "decade":
		var alts []string
		var args []any
		for _, r := range c.Ranges {
			alts = append(alts, i+`.year BETWEEN ? AND ?`)
			args = append(args, r[0], r[1])
		}
		return "(" + strings.Join(alts, " OR ") + ")", args
	case c.Field == "added_within_days":
		cutoff := now - int64(c.Days)*24*60*60*1000
		// A show counts as added when any of its episodes is.
		return `(` + i + `.added_at >= ? OR EXISTS (SELECT 1 FROM items s JOIN items e ON e.parent_id = s.id
			WHERE s.parent_id = ` + i + `.id AND e.missing_since IS NULL AND e.added_at >= ?))`, []any{cutoff, cutoff}
	}
	return "0", nil
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
