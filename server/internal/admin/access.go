package admin

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Label is a label that exists on at least one title, for the policy editor's
// allow and deny lists.
type Label struct {
	Name      string `json:"name"`
	Hidden    bool   `json:"hidden"`
	ItemCount int    `json:"item_count"`
}

// Labels lists every label with how many titles carry it, most used first.
func (a *Admin) Labels(ctx context.Context) ([]Label, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT l.name, l.hidden, COUNT(il.item_id) FROM labels l
		LEFT JOIN item_labels il ON il.label_id = l.id GROUP BY l.id ORDER BY COUNT(il.item_id) DESC, l.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.Name, &l.Hidden, &l.ItemCount); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// normalizePolicy checks a policy before it is saved: a known resolution and
// rating (stored in their canonical spelling, so the SQL tables match them),
// and label lists without blanks or repeats.
func normalizePolicy(p *UserPolicy) error {
	if p.MaxResolution != nil && *p.MaxResolution == "" {
		p.MaxResolution = nil
	}
	if p.MaxResolution != nil {
		r := policy.ResolutionRank(*p.MaxResolution)
		if r < 0 {
			return fmt.Errorf("max_resolution must be one of %s", strings.Join(policy.Resolutions, ", "))
		}
		p.MaxResolution = &policy.Resolutions[r]
	}
	if p.MaxContentRating != nil && strings.TrimSpace(*p.MaxContentRating) == "" {
		p.MaxContentRating = nil
	}
	if p.MaxContentRating != nil {
		i := slices.IndexFunc(policy.ContentRatings, func(r string) bool { return strings.EqualFold(r, strings.TrimSpace(*p.MaxContentRating)) })
		if i < 0 {
			return fmt.Errorf("max_content_rating must be one of %s", strings.Join(policy.ContentRatings, ", "))
		}
		p.MaxContentRating = &policy.ContentRatings[i]
	}
	p.AllowLabels, p.DenyLabels = cleanLabels(p.AllowLabels), cleanLabels(p.DenyLabels)
	return nil
}

func cleanLabels(in []string) []string {
	out := []string{}
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l != "" && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, l) }) {
			out = append(out, l)
		}
	}
	return out
}

// Summary says in one line what a user can see and play, for the Users
// settings: "Sees Movies and TV, up to 1080p and PG-13, never titles labelled
// horror." names maps library ids to names.
func Summary(u User, names map[int64]string) string {
	switch {
	case u.Disabled:
		return "Disabled: cannot sign in."
	case u.IsAdmin:
		return "Admin: sees every library and every version, and manages the server."
	}
	var libs []string
	for _, id := range u.LibraryIDs {
		if n, ok := names[id]; ok {
			libs = append(libs, n)
		}
	}
	if len(libs) == 0 {
		return "Sees nothing yet: no libraries granted."
	}
	p := u.Policy
	parts := []string{"Sees " + andList(libs, "and")}
	var limits []string
	if p.MaxResolution != nil {
		limits = append(limits, *p.MaxResolution)
	}
	if p.MaxContentRating != nil {
		limits = append(limits, *p.MaxContentRating)
	}
	if len(limits) > 0 {
		parts = append(parts, "up to "+andList(limits, "and"))
	}
	if len(p.AllowLabels) > 0 {
		parts = append(parts, "only titles labelled "+andList(p.AllowLabels, "or"))
	}
	if len(p.DenyLabels) > 0 {
		parts = append(parts, "never titles labelled "+andList(p.DenyLabels, "or"))
	}
	switch {
	case !p.AllowTranscode:
		parts = append(parts, "no converted streams")
	case p.Allow4KTranscode:
		parts = append(parts, "may convert 4K")
	}
	return strings.Join(parts, ", ") + "."
}

// andList joins ["a", "b", "c"] as "a, b and c".
func andList(items []string, conj string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + conj + " " + items[len(items)-1]
}
