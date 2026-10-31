package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Newsletter is the event name of the weekly recently-added digest.
const Newsletter = "newsletter"

// Digest is what was added to the libraries in one window: movies, and new
// episodes grouped by show.
type Digest struct {
	Preview bool          `json:"preview"`
	Since   int64         `json:"since"`
	Until   int64         `json:"until"`
	Movies  []DigestMovie `json:"movies"`
	Shows   []DigestShow  `json:"shows"`
}

type DigestMovie struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	// PosterURL is TMDb's copy of the poster, which a chat service can fetch.
	// Posters that only exist on the server's disk have none.
	PosterURL string `json:"poster_url,omitempty"`
	AddedAt   int64  `json:"added_at"`
}

type DigestShow struct {
	ID        int64           `json:"id"`
	Title     string          `json:"title"`
	PosterURL string          `json:"poster_url,omitempty"`
	Episodes  []DigestEpisode `json:"episodes"`
}

type DigestEpisode struct {
	ID         int64  `json:"id"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	EpisodeEnd int    `json:"episode_end,omitempty"`
	Title      string `json:"title"`
	AddedAt    int64  `json:"added_at"`
}

// Episodes counts the new episodes across every show.
func (d Digest) Episodes() int {
	n := 0
	for _, s := range d.Shows {
		n += len(s.Episodes)
	}
	return n
}

// Empty means nothing was added, so there is nothing to send.
func (d Digest) Empty() bool { return len(d.Movies) == 0 && d.Episodes() == 0 }

// Text is the one-line summary, such as "New on VCRX this week: 2 movies and
// 5 episodes of 2 shows".
func (d Digest) Text(server string) string {
	var parts []string
	if n := len(d.Movies); n > 0 {
		parts = append(parts, plural(n, "movie", "movies"))
	}
	if n := d.Episodes(); n > 0 {
		parts = append(parts, plural(n, "episode", "episodes")+" of "+plural(len(d.Shows), "show", "shows"))
	}
	what := "nothing new"
	if len(parts) > 0 {
		what = strings.Join(parts, " and ")
	}
	pre := ""
	if d.Preview {
		pre = "Preview: "
	}
	return fmt.Sprintf("%sNew on %s this week: %s", pre, server, what)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Label is a movie's name as lists show it: the title and the year.
func (m DigestMovie) Label() string {
	if m.Year > 0 {
		return fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	return m.Title
}

// Code is S01E02, or S01E02-E03 for a file holding two episodes.
func (e DigestEpisode) Code() string {
	c := fmt.Sprintf("S%02dE%02d", e.Season, e.Episode)
	if e.EpisodeEnd > e.Episode {
		c += fmt.Sprintf("-E%02d", e.EpisodeEnd)
	}
	return c
}

// Discord's limits for one webhook message. A message over any of them is
// refused whole, so the digest is cut to fit rather than sent and lost.
const (
	discordMaxEmbeds     = 10
	discordMaxEmbedTotal = 6000 // titles, descriptions and footers of every embed together
	discordMaxTitle      = 256
	discordMaxContent    = 2000
	// Below Discord's 4,096 per description, so a few long embeds cannot use up
	// the 6,000 shared by all ten.
	digestMaxDescription = 1000
	digestEpisodesShown  = 10
	digestColor          = 0xff5f00
)

// discordDigest builds one Discord message: an embed with a poster per movie
// and per show, and when there are more than fit, a last embed listing the
// rest by name and ending "and N more".
func discordDigest(d Digest, server string) map[string]any {
	type entry struct {
		embed map[string]any
		name  string
		size  int
	}
	var entries []entry
	for _, m := range d.Movies {
		e := map[string]any{"title": cut(m.Label(), discordMaxTitle), "description": "New movie", "color": digestColor}
		if m.PosterURL != "" {
			e["thumbnail"] = map[string]string{"url": m.PosterURL}
		}
		entries = append(entries, entry{e, m.Label(), embedSize(e)})
	}
	for _, s := range d.Shows {
		lines := make([]string, 0, len(s.Episodes))
		for _, ep := range s.Episodes {
			line := ep.Code()
			if ep.Title != "" {
				line += " · " + ep.Title
			}
			lines = append(lines, cut(line, 120))
		}
		e := map[string]any{
			"title":       cut(s.Title, discordMaxTitle),
			"description": joinLimited(lines, "\n", digestEpisodesShown, digestMaxDescription, "episodes"),
			"color":       digestColor,
		}
		if s.PosterURL != "" {
			e["thumbnail"] = map[string]string{"url": s.PosterURL}
		}
		entries = append(entries, entry{e, s.Title, embedSize(e)})
	}

	// Take as many whole entries as fit, keeping one embed back for the rest.
	shown := min(len(entries), discordMaxEmbeds)
	var more map[string]any
	for ; shown >= 0; shown-- {
		if shown < len(entries) && shown == discordMaxEmbeds {
			continue // ten entries and a list of the rest would be eleven embeds
		}
		more = nil
		total := 0
		for _, e := range entries[:shown] {
			total += e.size
		}
		if rest := entries[shown:]; len(rest) > 0 {
			names := make([]string, len(rest))
			for i, e := range rest {
				names[i] = cut(e.name, 120)
			}
			more = map[string]any{
				"title":       fmt.Sprintf("And %d more", len(rest)),
				"description": joinLimited(names, ", ", len(names), digestMaxDescription, ""),
				"color":       digestColor,
			}
			total += embedSize(more)
		}
		if total <= discordMaxEmbedTotal {
			break
		}
	}
	embeds := make([]map[string]any, 0, discordMaxEmbeds)
	for _, e := range entries[:shown] {
		embeds = append(embeds, e.embed)
	}
	if more != nil {
		embeds = append(embeds, more)
	}
	return map[string]any{"content": cut(d.Text(server), discordMaxContent), "username": cut(server, 80), "embeds": embeds}
}

// embedSize counts the characters Discord counts toward its 6,000 limit.
func embedSize(e map[string]any) int {
	n := 0
	for _, k := range []string{"title", "description"} {
		if s, ok := e[k].(string); ok {
			n += len([]rune(s))
		}
	}
	return n
}

// joinLimited joins at most shown items and stops before limit characters,
// then says how many it left out: "and 4 more episodes".
func joinLimited(items []string, sep string, shown, limit int, noun string) string {
	var b strings.Builder
	used := 0
	for i, it := range items {
		left := len(items) - i
		tail := moreText(left, noun)
		next := it
		if i > 0 {
			next = sep + it
		}
		// Room for this item and, if it is not the last, the "and N more" after it.
		need := len([]rune(next))
		if left > 1 {
			need += len([]rune(sep + tail))
		}
		if i >= shown || used+need > limit {
			if i > 0 {
				tail = sep + tail
			}
			b.WriteString(tail)
			return b.String()
		}
		b.WriteString(next)
		used += len([]rune(next))
	}
	return b.String()
}

func moreText(n int, noun string) string {
	if noun == "" {
		return fmt.Sprintf("and %d more", n)
	}
	return fmt.Sprintf("and %d more %s", n, noun)
}

// cut shortens s to at most n characters, ending with an ellipsis when it had to.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// SendDigest posts the digest to every webhook and waits for the answers,
// so the caller knows whether it went out. It returns how many accepted it.
// Like every other event it is held back when webhook_events leaves it out.
func (w *Webhooks) SendDigest(ctx context.Context, d Digest) (int, error) {
	if !w.Wants(Newsletter) {
		return 0, errors.New("no webhook takes the newsletter event")
	}
	if d.Movies == nil {
		d.Movies = []DigestMovie{}
	}
	if d.Shows == nil {
		d.Shows = []DigestShow{}
	}
	plain := struct {
		Event   string `json:"event"`
		Content string `json:"content"`
		Server  string `json:"server"`
		Time    int64  `json:"time"`
		Digest
	}{Newsletter, d.Text(w.Server), w.Server, time.Now().UnixMilli(), d}
	sent := 0
	var errs []error
	for _, u := range w.URLs {
		var body any = plain
		if isDiscord(u) {
			body = discordDigest(d, w.Server)
		}
		if err := w.postSync(ctx, u, body); err != nil {
			w.Log.Warn("newsletter webhook failed", "host", host(u), "err", err)
			errs = append(errs, err)
			continue
		}
		sent++
	}
	return sent, errors.Join(errs...)
}

// postSync posts one JSON body and reports failure, with the webhook's token
// kept out of the error.
func (w *Webhooks) postSync(ctx context.Context, target string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%s: bad url", host(target))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vcrx-media-server")
	resp, err := w.Client.Do(req)
	if err != nil {
		return errors.New(redact(err.Error(), target))
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s answered %d", host(target), resp.StatusCode)
	}
	return nil
}
