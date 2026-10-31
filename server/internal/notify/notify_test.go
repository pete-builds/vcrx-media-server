package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSendPostsJSONAndFiltersEvents(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer srv.Close()
	w := New([]string{srv.URL + "/hook"}, []string{Play}, "VCRX", nil)
	w.Send(Notice{Event: Stop, Username: "pete", Title: "Film"})
	w.Send(Notice{Event: Play, Username: "pete", Title: "Film", Device: "Living Room", Decision: "direct_play"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d posts, want only the play event", len(got))
	}
	if got[0]["event"] != "play" || got[0]["server"] != "VCRX" || !strings.Contains(got[0]["content"].(string), "pete started Film on Living Room") {
		t.Fatalf("payload: %v", got[0])
	}
}

func TestDiscordShape(t *testing.T) {
	if !isDiscord("https://discord.com/api/webhooks/1/abc") || isDiscord("https://example.com/api/webhooks/1/abc") ||
		isDiscord("https://discord.com.evil.example/api/webhooks/1") {
		t.Fatal("discord detection")
	}
	if host("https://discord.com/api/webhooks/1/secret") != "discord.com" {
		t.Fatal("host must drop the token path")
	}
}

func TestText(t *testing.T) {
	n := Notice{Event: Stop, Username: "pete", Title: "Film", PositionMs: 45, DurationMs: 100, PlayedMs: 61 * 60_000}
	if got := n.Text(); got != "■ pete stopped Film at 45%, watched 1h 01m" {
		t.Fatalf("stop text: %q", got)
	}
}

func TestAddedText(t *testing.T) {
	n := Notice{Event: Added, Library: "Movies", Message: AddedSummary([]string{"A (2001)", "B", "C", "D", "E", "F", "G"})}
	if got := n.Text(); got != "＋ New in Movies: A (2001), B, C, D, E and 2 more" {
		t.Fatalf("%q", got)
	}
}
