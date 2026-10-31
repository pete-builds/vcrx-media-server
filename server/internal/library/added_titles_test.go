package library

import (
	"context"
	"reflect"
	"testing"
)

// A scan that adds titles calls OnAdded once, naming only what is new: a
// second scan with nothing new stays quiet, and episodes group by show.
func TestOnAddedNamesOnlyNewTitles(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{"Nosferatu (1922)/Nosferatu.1922.mkv": "a"})
	var got [][]string
	e.sc.OnAdded = func(ctx context.Context, lib Library, run Run, idsAbove int64) {
		titles, err := AddedTitles(ctx, e.db, lib.ID, idsAbove)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, titles)
	}
	e.scan(Options{})
	e.scan(Options{}) // nothing new: no call
	e.write(map[string]string{"The General (1926)/The.General.1926.mkv": "b"})
	e.scan(Options{})
	want := [][]string{{"Nosferatu (1922)"}, {"The General (1926)"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OnAdded calls %v, want %v", got, want)
	}

	tv := newEnv(t, KindShow, "folder", map[string]string{"Quiet Street/Season 01/Quiet Street - S01E01.mkv": "a"})
	var titles []string
	tv.sc.OnAdded = func(ctx context.Context, lib Library, run Run, idsAbove int64) {
		titles, _ = AddedTitles(ctx, tv.db, lib.ID, idsAbove)
	}
	tv.scan(Options{})
	if !reflect.DeepEqual(titles, []string{"Quiet Street S01E01"}) {
		t.Fatalf("one episode: %v", titles)
	}
	tv.write(map[string]string{"Quiet Street/Season 01/Quiet Street - S01E02.mkv": "b", "Quiet Street/Season 01/Quiet Street - S01E03.mkv": "c"})
	tv.scan(Options{})
	if !reflect.DeepEqual(titles, []string{"Quiet Street (2 new episodes)"}) {
		t.Fatalf("two episodes: %v", titles)
	}
}
