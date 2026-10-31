package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func movies(n int, title string) []DigestMovie {
	out := make([]DigestMovie, n)
	for i := range out {
		out[i] = DigestMovie{ID: int64(i + 1), Title: fmt.Sprintf("%s %d", title, i+1), Year: 2020, PosterURL: "https://image.example/p.jpg"}
	}
	return out
}

func embeds(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	b, _ := json.Marshal(msg)
	var out struct {
		Content string           `json:"content"`
		Embeds  []map[string]any `json:"embeds"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(out.Content)); n > discordMaxContent {
		t.Fatalf("content is %d characters", n)
	}
	return out.Embeds
}

// checkLimits fails when a message breaks any of Discord's embed limits.
func checkLimits(t *testing.T, list []map[string]any) {
	t.Helper()
	if len(list) > discordMaxEmbeds {
		t.Fatalf("%d embeds, Discord allows %d", len(list), discordMaxEmbeds)
	}
	total := 0
	for _, e := range list {
		title, _ := e["title"].(string)
		desc, _ := e["description"].(string)
		if n := len([]rune(title)); n > discordMaxTitle {
			t.Fatalf("title of %d characters", n)
		}
		if n := len([]rune(desc)); n > 4096 {
			t.Fatalf("description of %d characters", n)
		}
		total += len([]rune(title)) + len([]rune(desc))
	}
	if total > discordMaxEmbedTotal {
		t.Fatalf("embeds total %d characters, Discord allows %d", total, discordMaxEmbedTotal)
	}
}

func TestDigestText(t *testing.T) {
	d := Digest{Movies: movies(2, "Film"), Shows: []DigestShow{
		{Title: "Quiet Street", Episodes: []DigestEpisode{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}}},
		{Title: "Harbor", Episodes: []DigestEpisode{{Season: 2, Episode: 5}}},
	}}
	if got := d.Text("VCRX"); got != "New on VCRX this week: 2 movies and 3 episodes of 2 shows" {
		t.Fatalf("%q", got)
	}
	one := Digest{Preview: true, Shows: []DigestShow{{Title: "Harbor", Episodes: []DigestEpisode{{Season: 2, Episode: 5}}}}}
	if got := one.Text("VCRX"); got != "Preview: New on VCRX this week: 1 episode of 1 show" {
		t.Fatalf("%q", got)
	}
	if (DigestEpisode{Season: 1, Episode: 2, EpisodeEnd: 3}).Code() != "S01E02-E03" {
		t.Fatal("multi-episode code")
	}
}

func TestDiscordDigestFitsInOneMessage(t *testing.T) {
	// Few entries: one embed each, with the poster, nothing cut.
	small := discordDigest(Digest{Movies: movies(2, "Film"), Shows: []DigestShow{{Title: "Quiet Street", PosterURL: "https://image.example/s.jpg",
		Episodes: []DigestEpisode{{Season: 1, Episode: 1, Title: "Pilot"}, {Season: 1, Episode: 2}}}}}, "VCRX")
	list := embeds(t, small)
	if len(list) != 3 || list[0]["title"] != "Film 1 (2020)" || list[2]["description"] != "S01E01 · Pilot\nS01E02" ||
		list[2]["thumbnail"].(map[string]any)["url"] != "https://image.example/s.jpg" {
		t.Fatalf("small digest: %v", list)
	}

	// 25 movies: nine embeds, then "And 16 more" naming the rest.
	list = embeds(t, discordDigest(Digest{Movies: movies(25, "Film")}, "VCRX"))
	checkLimits(t, list)
	last := list[len(list)-1]
	if len(list) != 10 || last["title"] != "And 16 more" || !strings.HasPrefix(last["description"].(string), "Film 10 (2020), Film 11 (2020)") {
		t.Fatalf("25 movies: %d embeds, last %v", len(list), last)
	}

	// Exactly ten fit as ten, with no "more" embed.
	list = embeds(t, discordDigest(Digest{Movies: movies(10, "Film")}, "VCRX"))
	if len(list) != 10 || list[9]["title"] != "Film 10 (2020)" {
		t.Fatalf("ten movies: %v", list[len(list)-1])
	}

	// A show with 30 episodes lists ten and says how many it left out.
	var eps []DigestEpisode
	for i := 1; i <= 30; i++ {
		eps = append(eps, DigestEpisode{Season: 1, Episode: i, Title: "An episode"})
	}
	list = embeds(t, discordDigest(Digest{Shows: []DigestShow{{Title: "Long Show", Episodes: eps}}}, "VCRX"))
	if d := list[0]["description"].(string); strings.Count(d, "\n") != 10 || !strings.HasSuffix(d, "and 20 more episodes") {
		t.Fatalf("long show: %q", d)
	}

	// Long names everywhere: the 6,000 character total still holds, and the
	// overflow list ends with how many it could not name.
	long := strings.Repeat("Very Long Title ", 30)
	var shows []DigestShow
	for i := range 40 {
		var eps []DigestEpisode
		for e := 1; e <= 12; e++ {
			eps = append(eps, DigestEpisode{Season: 1, Episode: e, Title: long})
		}
		shows = append(shows, DigestShow{Title: fmt.Sprintf("%s %d", long, i), Episodes: eps})
	}
	// Shows only: their episode lists are what fill the 6,000 characters.
	list = embeds(t, discordDigest(Digest{Shows: shows}, "VCRX"))
	checkLimits(t, list)
	last = list[len(list)-1]
	if !strings.HasPrefix(last["title"].(string), "And ") || !strings.Contains(last["description"].(string), " more") {
		t.Fatalf("overflow embed: %v", last)
	}
	if len([]rune(list[0]["title"].(string))) != discordMaxTitle {
		t.Fatalf("long title not cut to %d", discordMaxTitle)
	}
}

func TestJoinLimited(t *testing.T) {
	if got := joinLimited([]string{"a", "b", "c"}, ", ", 3, 100, ""); got != "a, b, c" {
		t.Fatalf("%q", got)
	}
	if got := joinLimited([]string{"a", "b", "c", "d"}, ", ", 2, 100, "titles"); got != "a, b, and 2 more titles" {
		t.Fatalf("%q", got)
	}
	// Within 20 characters a second name would leave no room to say what was left out.
	if got := joinLimited([]string{"aaaa", "bbbb", "cccc", "dddd"}, ", ", 9, 20, ""); got != "aaaa, and 3 more" {
		t.Fatalf("%q", got)
	}
}

func TestSendDigestPlainJSON(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
	}))
	defer srv.Close()
	w := New([]string{srv.URL + "/hook"}, []string{Play}, "VCRX", nil)
	n, err := w.SendDigest(context.Background(), Digest{Movies: movies(1, "Film")})
	if n != 1 || err != nil {
		t.Fatalf("sent %d, %v", n, err)
	}
	// webhook_events limits playback events, not the newsletter, which is switched on by itself.
	if got["event"] != Newsletter || got["content"] != "New on VCRX this week: 1 movie" || len(got["movies"].([]any)) != 1 ||
		got["shows"] == nil || got["movies"].([]any)[0].(map[string]any)["poster_url"] != "https://image.example/p.jpg" {
		t.Fatalf("payload: %v", got)
	}

	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer refuse.Close()
	w = New([]string{refuse.URL + "/secret-token"}, nil, "VCRX", nil)
	if n, err := w.SendDigest(context.Background(), Digest{Movies: movies(1, "Film")}); n != 0 || err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("refused: %d %v", n, err)
	}
	if n, err := (&Webhooks{}).SendDigest(context.Background(), Digest{}); n != 0 || err == nil {
		t.Fatal("no URLs must be an error")
	}
}
