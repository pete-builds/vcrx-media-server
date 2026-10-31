// Package policy decides what a user may see and play. Every catalog query and
// every stream request goes through it, server-side; clients are never trusted
// to hide anything.
package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Resolutions in ascending order; the index is the rank.
var Resolutions = []string{"SD", "480p", "576p", "720p", "1080p", "4K", "8K"}

func ResolutionRank(r string) int {
	for i, v := range Resolutions {
		if strings.EqualFold(v, r) {
			return i
		}
	}
	return -1
}

// ContentRatings are the ceilings a policy may set, mildest first. Each tier
// also admits the TV ratings for the same audience, the way Plex's restriction
// profiles group them: a PG-13 ceiling allows TV-14, a PG ceiling TV-PG.
var ContentRatings = []string{"G", "PG", "PG-13", "R", "NC-17"}

// ratingTiers maps every rating the scanner and TMDb write (US certificates,
// after metadata.ContentRating strips "Rated " and country prefixes) to its tier
// in ContentRatings. Anything else, "NR" and "Unrated" included, has no tier.
var ratingTiers = map[string]int{
	"G": 0, "TV-Y": 0, "TV-G": 0,
	"PG": 1, "TV-Y7": 1, "TV-Y7-FV": 1, "TV-PG": 1,
	"PG-13": 2, "TV-14": 2,
	"R": 3, "TV-MA": 3,
	"NC-17": 4, "X": 4,
}

// ContentRatingRank is a rating's tier, or -1 when it is not a known rating.
func ContentRatingRank(r string) int {
	if t, ok := ratingTiers[strings.ToUpper(strings.TrimSpace(r))]; ok {
		return t
	}
	return -1
}

type Policy struct {
	UserID  int64
	IsAdmin bool
	// LibraryIDs are the granted libraries; ignored for admins, who see all.
	LibraryIDs       []int64
	MaxResolution    string
	MaxContentRating string
	AllowLabels      []string
	DenyLabels       []string
	AllowTranscode   bool
	Allow4KTranscode bool
}

// Load reads a user's grants and policy row. A missing policy row means no extra
// restrictions beyond library grants.
func Load(ctx context.Context, conn *sql.DB, userID int64, isAdmin bool) (Policy, error) {
	p := Policy{UserID: userID, IsAdmin: isAdmin, AllowTranscode: true, Allow4KTranscode: isAdmin}
	var err error
	if p.LibraryIDs, err = grants(ctx, conn, userID); err != nil {
		return p, err
	}
	var maxRes, maxRating sql.NullString
	var allow, deny string
	err = conn.QueryRowContext(ctx, `SELECT max_resolution, max_content_rating, allow_labels, deny_labels, allow_transcode, allow_4k_transcode
		FROM user_policies WHERE user_id = ?`, userID).Scan(&maxRes, &maxRating, &allow, &deny, &p.AllowTranscode, &p.Allow4KTranscode)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.MaxResolution = maxRes.String
	p.MaxContentRating = maxRating.String
	_ = json.Unmarshal([]byte(allow), &p.AllowLabels)
	_ = json.Unmarshal([]byte(deny), &p.DenyLabels)
	return p, nil
}

func grants(ctx context.Context, conn *sql.DB, userID int64) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, `SELECT library_id FROM library_grants WHERE user_id = ? ORDER BY library_id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CanSeeLibrary reports whether the library is granted.
func (p Policy) CanSeeLibrary(id int64) bool {
	if p.IsAdmin {
		return true
	}
	for _, l := range p.LibraryIDs {
		if l == id {
			return true
		}
	}
	return false
}

// resolutionRankSQL maps a resolution column to its rank, NULL to -1.
func resolutionRankSQL(col string) string {
	var b strings.Builder
	b.WriteString("(CASE " + col)
	for i, r := range Resolutions {
		b.WriteString(" WHEN '" + r + "' THEN ")
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString(" ELSE -1 END)")
	return b.String()
}

// ratingRankSQL maps a content rating expression to its tier, unknown to -1.
// The values come from the fixed table above, never from a request.
func ratingRankSQL(expr string) string {
	keys := make([]string, 0, len(ratingTiers))
	for k := range ratingTiers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("(CASE UPPER(TRIM(" + expr + "))")
	for _, k := range keys {
		b.WriteString(" WHEN '" + k + "' THEN " + strconv.Itoa(ratingTiers[k]))
	}
	b.WriteString(" ELSE -1 END)")
	return b.String()
}

// ratingCond hides items above the content-rating ceiling. A season or episode
// is judged by its show's rating, and by its own when it has a stricter one; a
// title with no known rating anywhere is hidden, like an unprobed version, so
// an unrated horror film cannot slip past a PG-13 ceiling.
func (p Policy) ratingCond(i string) (string, []any) {
	if p.IsAdmin || p.MaxContentRating == "" {
		return "", nil
	}
	ceiling := ContentRatingRank(p.MaxContentRating)
	if ceiling < 0 {
		// An unknown ceiling cannot be honoured; fail closed rather than open.
		return "0", nil
	}
	show := `(CASE ` + i + `.kind
		WHEN 'season' THEN (SELECT rs.content_rating FROM items rs WHERE rs.id = ` + i + `.parent_id)
		WHEN 'episode' THEN (SELECT rg.content_rating FROM items rp JOIN items rg ON rg.id = rp.parent_id WHERE rp.id = ` + i + `.parent_id)
		ELSE ` + i + `.content_rating END)`
	lineage := ratingRankSQL("COALESCE(NULLIF(" + show + ", ''), " + i + ".content_rating, '')")
	own := ratingRankSQL("COALESCE(" + i + ".content_rating, '')")
	return "(" + lineage + " BETWEEN 0 AND ? AND " + own + " <= ?)", []any{ceiling, ceiling}
}

// VersionCond returns a SQL condition on a media_versions alias: present on disk
// and within the user's resolution ceiling. A version whose resolution is unknown
// is hidden from a restricted user, so an unprobed 4K file cannot slip through.
func (p Policy) VersionCond(v string) (string, []any) {
	cond := v + ".missing_since IS NULL"
	if p.MaxResolution != "" && !p.IsAdmin {
		rank := ResolutionRank(p.MaxResolution)
		cond += " AND " + resolutionRankSQL(v+".resolution") + " BETWEEN 0 AND ?"
		return cond, []any{rank}
	}
	return cond, nil
}

// ItemCond returns a SQL condition on an items alias: in a granted library, not
// missing, not excluded by labels or the content-rating ceiling, and holding at
// least one playable version (directly, or through its episodes for a show or season).
func (p Policy) ItemCond(i string) (string, []any) {
	var conds []string
	var args []any
	conds = append(conds, i+".missing_since IS NULL")
	if !p.IsAdmin {
		if len(p.LibraryIDs) == 0 {
			return "0", nil
		}
		conds = append(conds, i+".library_id IN ("+placeholders(len(p.LibraryIDs))+")")
		for _, id := range p.LibraryIDs {
			args = append(args, id)
		}
	}
	vc, vargs := p.VersionCond("pv")
	// A season or show also needs an episode the user may see by the episode's
	// own rating and labels (its show's are checked on the item itself), so a
	// season of nothing but hidden episodes is hidden too.
	seasonEp, seasonArgs := p.episodeCond("pe.id")
	showEp, showArgs := p.episodeCond("pe.id, ps.id")
	playable := `(CASE ` + i + `.kind
		WHEN 'movie' THEN EXISTS (SELECT 1 FROM media_versions pv WHERE pv.item_id = ` + i + `.id AND ` + vc + `)
		WHEN 'episode' THEN EXISTS (SELECT 1 FROM media_versions pv WHERE pv.item_id = ` + i + `.id AND ` + vc + `)
		WHEN 'season' THEN EXISTS (SELECT 1 FROM items pe JOIN media_versions pv ON pv.item_id = pe.id
			WHERE pe.parent_id = ` + i + `.id AND pe.missing_since IS NULL AND ` + vc + seasonEp + `)
		ELSE EXISTS (SELECT 1 FROM items ps JOIN items pe ON pe.parent_id = ps.id JOIN media_versions pv ON pv.item_id = pe.id
			WHERE ps.parent_id = ` + i + `.id AND pe.missing_since IS NULL AND ` + vc + showEp + `)
		END)`
	conds = append(conds, playable)
	args = append(args, vargs...)
	args = append(args, vargs...)
	args = append(append(args, vargs...), seasonArgs...)
	args = append(append(args, vargs...), showArgs...)
	if rc, rargs := p.ratingCond(i); rc != "" {
		conds = append(conds, rc)
		args = append(args, rargs...)
	}
	if !p.IsAdmin && (len(p.DenyLabels) > 0 || len(p.AllowLabels) > 0) {
		// Labels set on a show apply to its seasons and episodes too.
		lineage := `(il.item_id = ` + i + `.id OR il.item_id = ` + i + `.parent_id
			OR il.item_id = (SELECT pp.parent_id FROM items pp WHERE pp.id = ` + i + `.parent_id))`
		if len(p.DenyLabels) > 0 {
			conds = append(conds, `NOT EXISTS (SELECT 1 FROM item_labels il JOIN labels l ON l.id = il.label_id
				WHERE `+lineage+` AND l.name IN (`+placeholders(len(p.DenyLabels))+`))`)
			for _, l := range p.DenyLabels {
				args = append(args, l)
			}
		}
		if len(p.AllowLabels) > 0 {
			conds = append(conds, `EXISTS (SELECT 1 FROM item_labels il JOIN labels l ON l.id = il.label_id
				WHERE `+lineage+` AND l.name IN (`+placeholders(len(p.AllowLabels))+`))`)
			for _, l := range p.AllowLabels {
				args = append(args, l)
			}
		}
	}
	return "(" + strings.Join(conds, " AND ") + ")", args
}

// episodeCond is the extra condition, starting " AND", on an episode alias pe
// inside a season's or show's playable check: the episode's own rating within
// the ceiling, and no denied label on any of ids (the episode, and its season
// when the check starts from the show).
func (p Policy) episodeCond(ids string) (string, []any) {
	if p.IsAdmin {
		return "", nil
	}
	var cond string
	var args []any
	if p.MaxContentRating != "" {
		cond += " AND " + ratingRankSQL("COALESCE(pe.content_rating, '')") + " <= ?"
		args = append(args, ContentRatingRank(p.MaxContentRating))
	}
	if len(p.DenyLabels) > 0 {
		cond += ` AND NOT EXISTS (SELECT 1 FROM item_labels pil JOIN labels pl ON pl.id = pil.label_id
			WHERE pil.item_id IN (` + ids + `) AND pl.name IN (` + placeholders(len(p.DenyLabels)) + `))`
		for _, l := range p.DenyLabels {
			args = append(args, l)
		}
	}
	return cond, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
