package collections

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// FileResult is one YAML file as loaded: its definitions, or why it was refused.
// A file with any error is refused whole, so a half-read file never changes
// what is built.
type FileResult struct {
	File string
	Defs []Definition
	Err  error
}

// LoadDir reads every *.yaml and *.yml file in dir, in name order. Files whose
// names start with a dot (editor swap files) are skipped. The error is for the
// directory itself; per-file errors are in each FileResult.
func LoadDir(dir string) ([]FileResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		ext := strings.ToLower(filepath.Ext(n))
		if e.IsDir() || strings.HasPrefix(n, ".") || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]FileResult, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n)) //nolint:gosec // the operator chooses this directory
		if err != nil {
			out = append(out, FileResult{File: n, Err: fmt.Errorf("%s: %w", n, err)})
			continue
		}
		defs, err := ParseFile(n, b)
		out = append(out, FileResult{File: n, Defs: defs, Err: err})
	}
	return out, nil
}

// ParseFile parses one collections file. Every error names the file and line.
func ParseFile(file string, data []byte) ([]Definition, error) {
	p := parser{file: file}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil // an empty file defines nothing
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, p.errf(root, "the file must be a mapping with a collections: list")
	}
	var defs []Definition
	err := p.fields(root, func(kn, val *yaml.Node) error {
		if kn.Value != "collections" {
			return p.unknown(kn, "collections")
		}
		if val.Kind != yaml.SequenceNode {
			return p.errf(val, "collections must be a list")
		}
		for _, n := range val.Content {
			d, err := p.definition(n)
			if err != nil {
				return err
			}
			defs = append(defs, d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]int{}
	for _, d := range defs {
		key := strings.ToLower(d.Library) + "\x00" + d.Slug()
		if line, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s:%d: collection %q in library %q is already defined on line %d", file, d.Line, d.Name, d.Library, line)
		}
		seen[key] = d.Line
	}
	return defs, nil
}

type parser struct{ file string }

func (p parser) errf(n *yaml.Node, format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.file, n.Line, fmt.Sprintf(format, args...))
}

// unknown refuses a key node that is not one of want.
func (p parser) unknown(kn *yaml.Node, want ...string) error {
	return p.errf(kn, "unknown field %q (want %s)", kn.Value, strings.Join(want, ", "))
}

// fields walks a mapping in order, refusing duplicate keys.
func (p parser) fields(m *yaml.Node, fn func(key *yaml.Node, val *yaml.Node) error) error {
	seen := map[string]bool{}
	for k := 0; k+1 < len(m.Content); k += 2 {
		kn, vn := m.Content[k], m.Content[k+1]
		if kn.Kind != yaml.ScalarNode {
			return p.errf(kn, "keys must be plain names")
		}
		if seen[kn.Value] {
			return p.errf(kn, "%q is set twice", kn.Value)
		}
		seen[kn.Value] = true
		if err := fn(kn, vn); err != nil {
			return err
		}
	}
	return nil
}

func (p parser) str(n *yaml.Node, what string) (string, error) {
	if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return "", p.errf(n, "%s must be a single value", what)
	}
	v := strings.TrimSpace(n.Value)
	if v == "" {
		return "", p.errf(n, "%s is empty", what)
	}
	return v, nil
}

func (p parser) boolean(n *yaml.Node, what string) (bool, error) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!bool" {
		var b bool
		if err := n.Decode(&b); err == nil {
			return b, nil
		}
	}
	return false, p.errf(n, "%s must be true or false", what)
}

func (p parser) definition(n *yaml.Node) (Definition, error) {
	d := Definition{File: p.file, Line: n.Line, Visible: defaultVisible}
	if n.Kind != yaml.MappingNode {
		return d, p.errf(n, "each collection must be a mapping with name, library and smart")
	}
	var hasSmart bool
	err := p.fields(n, func(kn, v *yaml.Node) error {
		var err error
		switch kn.Value {
		case "name":
			d.Name, err = p.str(v, "name")
		case "library":
			d.Library, err = p.str(v, "library")
		case "label":
			d.Label, err = p.str(v, "label")
		case "smart":
			hasSmart = true
			d.Smart, err = p.group(v, "smart")
		case "visible":
			err = p.visible(v, &d.Visible)
		default:
			err = p.unknown(kn, "name", "library", "smart", "label", "visible")
		}
		return err
	})
	if err != nil {
		return d, err
	}
	switch {
	case d.Name == "":
		return d, p.errf(n, "collection has no name")
	case d.Library == "":
		return d, p.errf(n, "collection %q has no library", d.Name)
	case !hasSmart:
		return d, p.errf(n, "collection %q has no smart rule", d.Name)
	case slugify(d.Name) == "":
		return d, p.errf(n, "collection name %q needs at least one letter or digit", d.Name)
	}
	return d, nil
}

func (p parser) visible(n *yaml.Node, v *Visible) error {
	if n.Kind != yaml.MappingNode {
		return p.errf(n, "visible must be a mapping of library, home and shared")
	}
	return p.fields(n, func(kn, val *yaml.Node) error {
		var err error
		switch kn.Value {
		case "library":
			v.Library, err = p.boolean(val, "visible.library")
		case "home":
			v.Home, err = p.boolean(val, "visible.home")
		case "shared":
			v.Shared, err = p.boolean(val, "visible.shared")
		default:
			err = p.unknown(kn, "library", "home", "shared")
		}
		return err
	})
}

// group parses a mapping of all and any lists.
func (p parser) group(n *yaml.Node, what string) (Rule, error) {
	var r Rule
	if n.Kind != yaml.MappingNode {
		return r, p.errf(n, "%s must be a mapping with all: or any:", what)
	}
	err := p.fields(n, func(kn, v *yaml.Node) error {
		key := kn.Value
		if key != "all" && key != "any" {
			if what == "smart" {
				return p.unknown(kn, "all", "any")
			}
			return p.errf(kn, "%q cannot sit beside all: or any: in one entry; put it in its own list entry", key)
		}
		if v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
			return p.errf(v, "%s must be a non-empty list", key)
		}
		for _, item := range v.Content {
			sub, err := p.rule(item)
			if err != nil {
				return err
			}
			if key == "all" {
				r.All = append(r.All, sub)
			} else {
				r.Any = append(r.Any, sub)
			}
		}
		return nil
	})
	if err == nil && len(r.All) == 0 && len(r.Any) == 0 {
		err = p.errf(n, "%s needs all: or any:", what)
	}
	return r, err
}

// rule parses one list entry: a nested group or exactly one condition.
func (p parser) rule(n *yaml.Node) (Rule, error) {
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return Rule{}, p.errf(n, "each rule must be a mapping such as genre: Horror")
	}
	for k := 0; k < len(n.Content); k += 2 {
		if key := n.Content[k].Value; key == "all" || key == "any" {
			return p.group(n, "a nested rule")
		}
	}
	if len(n.Content) > 2 {
		return Rule{}, p.errf(n, "one condition per list entry; combine conditions with all: or any:")
	}
	c, err := p.cond(n.Content[0], n.Content[1])
	if err != nil {
		return Rule{}, err
	}
	return Rule{Cond: &c}, nil
}

// values reads a single value or a list of them.
func (p parser) values(n *yaml.Node, field string) ([]string, error) {
	nodes := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		if len(n.Content) == 0 {
			return nil, p.errf(n, "%s needs at least one value", field)
		}
		nodes = n.Content
	}
	out := make([]string, 0, len(nodes))
	for _, v := range nodes {
		s, err := p.str(v, field)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

var (
	yearRange = regexp.MustCompile(`^(\d{4})\s*-\s*(\d{4})$`)
	decadeRe  = regexp.MustCompile(`^(\d{3}0)s?$`)
)

func (p parser) cond(kn, vn *yaml.Node) (Cond, error) {
	c := Cond{Field: kn.Value}
	if !tagFields[c.Field] {
		switch c.Field {
		case "decade", "year", "resolution", "hdr", "content_rating", "label", "added_within_days":
		default:
			return c, p.unknown(kn, Fields...)
		}
	}
	if c.Field == "added_within_days" {
		s, err := p.str(vn, c.Field)
		if err != nil {
			return c, err
		}
		d, err := strconv.Atoi(s)
		if err != nil || d < 1 || d > 36500 {
			return c, p.errf(vn, "added_within_days must be a whole number of days from 1 to 36500, not %q", s)
		}
		c.Days = d
		return c, nil
	}
	if c.Field == "hdr" && vn.Kind == yaml.ScalarNode && vn.Tag == "!!bool" {
		b, err := p.boolean(vn, "hdr")
		if err != nil {
			return c, err
		}
		if !b {
			return c, p.errf(vn, "hdr: false is not supported; leave the condition out")
		}
		c.Any = true
		return c, nil
	}
	vals, err := p.values(vn, c.Field)
	if err != nil {
		return c, err
	}
	for k, v := range vals {
		// Report the line of the offending list item when there is one.
		at := vn
		if vn.Kind == yaml.SequenceNode {
			at = vn.Content[k]
		}
		switch c.Field {
		case "resolution":
			r := policy.ResolutionRank(v)
			if r < 0 {
				return c, p.errf(at, "resolution %q is not one of %s", v, strings.Join(policy.Resolutions, ", "))
			}
			vals[k] = policy.Resolutions[r]
		case "hdr":
			ok := false
			for _, h := range hdrTypes {
				if strings.EqualFold(h, v) {
					vals[k], ok = h, true
				}
			}
			if !ok {
				return c, p.errf(at, "hdr %q is not true or one of %s", v, strings.Join(hdrTypes, ", "))
			}
		case "year":
			lo, hi, err := parseYears(v)
			if err != nil {
				return c, p.errf(at, "year %q: %v", v, err)
			}
			c.Ranges = append(c.Ranges, [2]int{lo, hi})
		case "decade":
			m := decadeRe.FindStringSubmatch(v)
			if m == nil {
				return c, p.errf(at, "decade %q must look like 1980s", v)
			}
			lo, _ := strconv.Atoi(m[1])
			c.Ranges = append(c.Ranges, [2]int{lo, lo + 9})
		}
	}
	if c.Field != "year" && c.Field != "decade" {
		c.Values = vals
	}
	return c, nil
}

// parseYears reads 1999 or 1990-1999.
func parseYears(v string) (int, int, error) {
	if m := yearRange.FindStringSubmatch(v); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		if lo > hi {
			return 0, 0, errors.New("the range runs backwards")
		}
		return lo, hi, nil
	}
	y, err := strconv.Atoi(v)
	if err != nil || y < 1000 || y > 9999 {
		return 0, 0, errors.New("want a year such as 1999 or a range such as 1990-1999")
	}
	return y, y, nil
}
