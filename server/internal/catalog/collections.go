package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Collection is a group of titles in one library, such as a film series.
type Collection struct {
	ID        int64  `json:"id"`
	LibraryID int64  `json:"library_id"`
	Name      string `json:"name"`
	ItemCount int    `json:"item_count"`
	// PosterIDs are up to four member items, for the collection's artwork.
	PosterIDs []int64 `json:"poster_item_ids"`
}

// CollectionRef names a collection an item belongs to.
type CollectionRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Count is one value of a filter and how many titles have it.
type Count struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Filters are the values a library can be narrowed by.
type Filters struct {
	Genres  []Count `json:"genres"`
	Decades []Count `json:"decades"`
}

// LibraryCollections lists a library's collections with the members the
// caller may see; a collection with none visible is left out.
func (c *Catalog) LibraryCollections(ctx context.Context, pol policy.Policy, libraryID int64) ([]Collection, error) {
	if !pol.CanSeeLibrary(libraryID) {
		return nil, ErrNotFound
	}
	cond, args := pol.ItemCond("i")
	rows, err := c.DB.QueryContext(ctx, `SELECT col.id, col.name, ci.item_id FROM collections col
		JOIN collection_items ci ON ci.collection_id = col.id JOIN items i ON i.id = ci.item_id
		WHERE col.library_id = ? AND `+cond+` ORDER BY col.sort_title COLLATE NOCASE, col.id, ci.position`,
		append([]any{libraryID}, args...)...)
	if err != nil {
		return nil, err
	}
	out := []Collection{}
	idx := map[int64]int{}
	err = each(rows, func() error {
		var id, item int64
		var name string
		if err := rows.Scan(&id, &name, &item); err != nil {
			return err
		}
		i, ok := idx[id]
		if !ok {
			out = append(out, Collection{ID: id, LibraryID: libraryID, Name: name, PosterIDs: []int64{}})
			i = len(out) - 1
			idx[id] = i
		}
		out[i].ItemCount++
		if len(out[i].PosterIDs) < 4 {
			out[i].PosterIDs = append(out[i].PosterIDs, item)
		}
		return nil
	})
	return out, err
}

// CollectionItems returns a collection and its visible members in order.
func (c *Catalog) CollectionItems(ctx context.Context, pol policy.Policy, userID, id int64) (Collection, []ItemSummary, error) {
	var col Collection
	err := c.DB.QueryRowContext(ctx, `SELECT id, library_id, name FROM collections WHERE id = ?`, id).Scan(&col.ID, &col.LibraryID, &col.Name)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !pol.CanSeeLibrary(col.LibraryID)) {
		return col, nil, ErrNotFound
	}
	if err != nil {
		return col, nil, err
	}
	cond, args := pol.ItemCond("i")
	ids, err := c.queryIDs(ctx, `SELECT i.id FROM collection_items ci JOIN items i ON i.id = ci.item_id
		WHERE ci.collection_id = ? AND `+cond+` ORDER BY ci.position`, append([]any{id}, args...)...)
	if err != nil {
		return col, nil, err
	}
	if len(ids) == 0 {
		return col, nil, ErrNotFound
	}
	items, err := c.Summaries(ctx, pol, userID, ids)
	col.ItemCount = len(items)
	col.PosterIDs = ids[:min(4, len(ids))]
	return col, items, err
}

// ItemCollections lists the collections an item is in.
func (c *Catalog) ItemCollections(ctx context.Context, itemID int64) ([]CollectionRef, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT col.id, col.name FROM collection_items ci JOIN collections col ON col.id = ci.collection_id
		WHERE ci.item_id = ? ORDER BY col.sort_title COLLATE NOCASE`, itemID)
	if err != nil {
		return nil, err
	}
	out := []CollectionRef{}
	err = each(rows, func() error {
		var r CollectionRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return err
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

// LibraryFilters counts the genres and decades of a library's visible titles.
func (c *Catalog) LibraryFilters(ctx context.Context, pol policy.Policy, libraryID int64) (Filters, error) {
	f := Filters{Genres: []Count{}, Decades: []Count{}}
	if !pol.CanSeeLibrary(libraryID) {
		return f, ErrNotFound
	}
	cond, args := pol.ItemCond("i")
	base := `FROM items i WHERE i.library_id = ? AND i.parent_id IS NULL AND ` + cond
	a := append([]any{libraryID}, args...)
	rows, err := c.DB.QueryContext(ctx, `SELECT t.value, COUNT(*) FROM item_tags t JOIN items i ON i.id = t.item_id
		WHERE t.kind = 'genre' AND i.library_id = ? AND i.parent_id IS NULL AND `+cond+` GROUP BY t.value`, a...)
	if err != nil {
		return f, err
	}
	err = each(rows, func() error {
		var v Count
		if err := rows.Scan(&v.Value, &v.Count); err != nil {
			return err
		}
		f.Genres = append(f.Genres, v)
		return nil
	})
	if err != nil {
		return f, err
	}
	sort.Slice(f.Genres, func(i, j int) bool { return f.Genres[i].Value < f.Genres[j].Value })
	rows, err = c.DB.QueryContext(ctx, `SELECT (i.year / 10) * 10 AS d, COUNT(*) `+base+` AND i.year IS NOT NULL GROUP BY d ORDER BY d DESC`, a...)
	if err != nil {
		return f, err
	}
	err = each(rows, func() error {
		var d int64
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return err
		}
		f.Decades = append(f.Decades, Count{Value: strconv.FormatInt(d, 10) + "s", Count: n})
		return nil
	})
	return f, err
}
