package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

const versionColumns = `v.id, v.item_id, v.library_id, v.path, v.label, v.container, v.size, v.bitrate,
	v.duration_ms, v.video_codec, v.video_profile, v.video_level, v.bit_depth, v.width, v.height, v.resolution,
	v.frame_rate, v.hdr_type, v.dv_profile, v.dv_compat_id, v.probe_status`

// Versions lists the versions of a movie or episode the caller may play, best first.
func (c *Catalog) Versions(ctx context.Context, pol policy.Policy, itemID int64) ([]Version, error) {
	vc, vargs := pol.VersionCond("v")
	rows, err := c.DB.QueryContext(ctx, `SELECT `+versionColumns+` FROM media_versions v WHERE v.item_id = ? AND `+vc,
		append([]any{itemID}, vargs...)...)
	if err != nil {
		return nil, err
	}
	vs, err := scanVersions(rows)
	if err != nil {
		return nil, err
	}
	if err := c.attachTracks(ctx, vs); err != nil {
		return nil, err
	}
	sort.SliceStable(vs, func(a, b int) bool {
		ra, rb := rank(vs[a].Resolution), rank(vs[b].Resolution)
		if ra != rb {
			return ra > rb
		}
		return deref(vs[a].Bitrate) > deref(vs[b].Bitrate)
	})
	return vs, nil
}

func rank(r *string) int {
	if r == nil {
		return -1
	}
	return policy.ResolutionRank(*r)
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func scanVersions(rows *sql.Rows) ([]Version, error) {
	out := []Version{}
	err := each(rows, func() error {
		var v Version
		var label, container, codec, profile, res, hdr sql.NullString
		var bitrate, dur, level, depth, w, h, dvp, dvc sql.NullInt64
		var fps sql.NullFloat64
		if err := rows.Scan(&v.ID, &v.ItemID, &v.LibraryID, &v.Path, &label, &container, &v.Size, &bitrate, &dur, &codec, &profile,
			&level, &depth, &w, &h, &res, &fps, &hdr, &dvp, &dvc, &v.ProbeStatus); err != nil {
			return err
		}
		v.FileName = filepath.Base(v.Path)
		v.Label, v.Container, v.VideoCodec, v.VideoProfile = strPtr(label), strPtr(container), strPtr(codec), strPtr(profile)
		v.Resolution, v.HDRType = strPtr(res), strPtr(hdr)
		v.Bitrate, v.DurationMs, v.VideoLevel, v.BitDepth = ptr(bitrate), ptr(dur), ptr(level), ptr(depth)
		v.Width, v.Height, v.DVProfile, v.DVCompatID = ptr(w), ptr(h), ptr(dvp), ptr(dvc)
		if fps.Valid {
			f := fps.Float64
			v.FrameRate = &f
		}
		v.AudioTracks, v.SubtitleTracks, v.VideoTracks = []Track{}, []Track{}, []Track{}
		out = append(out, v)
		return nil
	})
	return out, err
}

func (c *Catalog) attachTracks(ctx context.Context, vs []Version) error {
	if len(vs) == 0 {
		return nil
	}
	idx := map[int64]int{}
	ids := make([]int64, len(vs))
	for i, v := range vs {
		idx[v.ID] = i
		ids[i] = v.ID
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT id, version_id, kind, stream_index, codec, language, title, channels,
			is_default, is_forced, is_external, is_image_based
		FROM media_streams WHERE version_id IN (`+placeholders(len(ids))+`)
		ORDER BY version_id, is_external, stream_index, id`, int64Args(ids)...)
	if err != nil {
		return err
	}
	return each(rows, func() error {
		var t Track
		var vid int64
		var sidx, ch sql.NullInt64
		var codec, lang, title sql.NullString
		if err := rows.Scan(&t.ID, &vid, &t.Kind, &sidx, &codec, &lang, &title, &ch, &t.IsDefault, &t.IsForced, &t.IsExternal, &t.IsImageBased); err != nil {
			return err
		}
		t.Index, t.Channels = ptr(sidx), ptr(ch)
		t.Codec, t.Language, t.Title = strPtr(codec), strPtr(lang), strPtr(title)
		v := &vs[idx[vid]]
		switch t.Kind {
		case "audio":
			v.AudioTracks = append(v.AudioTracks, t)
		case "subtitle":
			v.SubtitleTracks = append(v.SubtitleTracks, t)
		default:
			v.VideoTracks = append(v.VideoTracks, t)
		}
		return nil
	})
}

// VersionForPlayback returns one version and its library root if the caller may
// play it.
func (c *Catalog) VersionForPlayback(ctx context.Context, pol policy.Policy, versionID int64) (Version, string, error) {
	vc, vargs := pol.VersionCond("v")
	cond, cargs := pol.ItemCond("i")
	rows, err := c.DB.QueryContext(ctx, `SELECT `+versionColumns+` FROM media_versions v JOIN items i ON i.id = v.item_id
		WHERE v.id = ? AND `+vc+` AND `+cond, append(append([]any{versionID}, vargs...), cargs...)...)
	if err != nil {
		return Version{}, "", err
	}
	vs, err := scanVersions(rows)
	if err != nil {
		return Version{}, "", err
	}
	if len(vs) == 0 {
		return Version{}, "", ErrNotFound
	}
	if err := c.attachTracks(ctx, vs); err != nil {
		return Version{}, "", err
	}
	var root string
	if err := c.DB.QueryRowContext(ctx, `SELECT root_path FROM libraries WHERE id = ?`, vs[0].LibraryID).Scan(&root); err != nil {
		return Version{}, "", err
	}
	return vs[0], root, nil
}
