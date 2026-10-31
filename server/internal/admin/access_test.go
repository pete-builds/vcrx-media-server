package admin

import "testing"

func TestSummary(t *testing.T) {
	names := map[int64]string{1: "Movies", 2: "TV", 3: "Kids"}
	s := func(v string) *string { return &v }
	for _, c := range []struct {
		u    User
		want string
	}{
		{User{IsAdmin: true}, "Admin: sees every library and every version, and manages the server."},
		{User{LibraryIDs: []int64{1}, Disabled: true}, "Disabled: cannot sign in."},
		{User{Policy: UserPolicy{AllowTranscode: true}}, "Sees nothing yet: no libraries granted."},
		{User{LibraryIDs: []int64{1, 2}, Policy: UserPolicy{MaxResolution: s("1080p"), MaxContentRating: s("PG-13"),
			DenyLabels: []string{"horror"}, AllowTranscode: true}},
			"Sees Movies and TV, up to 1080p and PG-13, never titles labelled horror."},
		{User{LibraryIDs: []int64{3, 1, 2}, Policy: UserPolicy{MaxContentRating: s("PG"), AllowLabels: []string{"family", "animated"}}},
			"Sees Kids, Movies and TV, up to PG, only titles labelled family or animated, no converted streams."},
		{User{LibraryIDs: []int64{2}, Policy: UserPolicy{AllowTranscode: true, Allow4KTranscode: true, DenyLabels: []string{"a", "b", "c"}}},
			"Sees TV, never titles labelled a, b or c, may convert 4K."},
	} {
		if got := Summary(c.u, names); got != c.want {
			t.Errorf("Summary(%+v)\n got %q\nwant %q", c.u, got, c.want)
		}
	}
}

func TestNormalizePolicy(t *testing.T) {
	s := func(v string) *string { return &v }
	p := UserPolicy{MaxResolution: s("4k"), MaxContentRating: s(" pg-13 "), DenyLabels: []string{" horror", "Horror", "", "gore"}}
	if err := normalizePolicy(&p); err != nil {
		t.Fatal(err)
	}
	if *p.MaxResolution != "4K" || *p.MaxContentRating != "PG-13" || len(p.DenyLabels) != 2 || p.DenyLabels[1] != "gore" || p.AllowLabels == nil {
		t.Fatalf("normalized: %+v %q %q", p, *p.MaxResolution, *p.MaxContentRating)
	}
	empty := UserPolicy{MaxResolution: s(""), MaxContentRating: s("")}
	if err := normalizePolicy(&empty); err != nil || empty.MaxResolution != nil || empty.MaxContentRating != nil {
		t.Fatalf("empty strings should clear the ceilings: %+v %v", empty, err)
	}
	for _, bad := range []UserPolicy{{MaxResolution: s("2K")}, {MaxContentRating: s("TV-MA")}, {MaxContentRating: s("PG13")}} {
		if err := normalizePolicy(&bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
