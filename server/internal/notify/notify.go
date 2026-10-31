// Package notify posts playback events to webhooks, the way Tautulli's
// notification agents do. A Discord webhook URL gets Discord's own message
// shape; any other URL gets the full event as JSON.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Event names.
const (
	Play       = "play"
	Stop       = "stop"
	Refused    = "refused"
	Terminated = "terminated"
	Added      = "added"
)

// Notice is one playback event.
type Notice struct {
	Event      string `json:"event"`
	SessionID  string `json:"session_id"`
	Username   string `json:"username"`
	Title      string `json:"title"`
	Version    string `json:"version,omitempty"`
	Device     string `json:"device,omitempty"`
	Client     string `json:"client,omitempty"`
	IP         string `json:"ip,omitempty"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason,omitempty"`
	PositionMs int64  `json:"position_ms"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	PlayedMs   int64  `json:"played_ms,omitempty"`
	Message    string `json:"message,omitempty"`
	// Library and Titles, for added.
	Library string   `json:"library,omitempty"`
	Titles  []string `json:"titles,omitempty"`
	Server  string   `json:"server"`
	Time    int64    `json:"time"`
}

// Text is the one-line summary used for Discord and for the JSON "content" field.
func (n Notice) Text() string {
	who, what := n.Username, n.Title
	on := ""
	if n.Device != "" {
		on = " on " + n.Device
	}
	switch n.Event {
	case Play:
		return fmt.Sprintf("▶ %s started %s%s (%s)", who, what, on, strings.ReplaceAll(n.Decision, "_", " "))
	case Stop:
		pct := ""
		if n.DurationMs > 0 {
			pct = fmt.Sprintf(" at %d%%", min(100, n.PositionMs*100/n.DurationMs))
		}
		return fmt.Sprintf("■ %s stopped %s%s%s, watched %s", who, what, on, pct, clock(n.PlayedMs))
	case Refused:
		return fmt.Sprintf("✕ %s could not play %s%s: %s", who, what, on, n.Reason)
	case Terminated:
		return fmt.Sprintf("■ Stopped %s's stream of %s%s: %s", who, what, on, n.Message)
	case Added:
		return fmt.Sprintf("＋ New in %s: %s", n.Library, n.Message)
	}
	return fmt.Sprintf("%s: %s %s", n.Event, who, what)
}

func clock(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

type Webhooks struct {
	URLs   []string
	Events map[string]bool // nil means every event
	Client *http.Client
	Log    *slog.Logger
	Server string
}

func New(urls []string, events []string, server string, log *slog.Logger) *Webhooks {
	w := &Webhooks{URLs: urls, Client: &http.Client{Timeout: 10 * time.Second}, Log: log, Server: server}
	if len(events) > 0 {
		w.Events = map[string]bool{}
		for _, e := range events {
			w.Events[strings.TrimSpace(e)] = true
		}
	}
	if w.Log == nil {
		w.Log = slog.Default()
	}
	return w
}

// Wants reports whether any webhook takes this event: there is at least one
// URL, and webhook_events, when set, names it.
func (w *Webhooks) Wants(event string) bool {
	return w != nil && len(w.URLs) > 0 && (w.Events == nil || w.Events[event])
}

// Send posts n to every webhook in the background. A failing webhook is logged
// and never slows playback.
func (w *Webhooks) Send(n Notice) {
	if w == nil || len(w.URLs) == 0 || (w.Events != nil && !w.Events[n.Event]) {
		return
	}
	n.Server = w.Server
	if n.Time == 0 {
		n.Time = time.Now().UnixMilli()
	}
	for _, u := range w.URLs {
		go w.post(u, n)
	}
}

func (w *Webhooks) post(target string, n Notice) {
	var body any = struct {
		Content string `json:"content"`
		Notice
	}{n.Text(), n}
	if isDiscord(target) {
		body = map[string]any{"content": n.Text(), "username": n.Server}
	}
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		w.Log.Warn("webhook: bad url", "host", host(target), "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vcrx-media-server")
	resp, err := w.Client.Do(req)
	if err != nil {
		w.Log.Warn("webhook failed", "host", host(target), "event", n.Event, "err", redact(err.Error(), target))
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		w.Log.Warn("webhook refused", "host", host(target), "event", n.Event, "status", resp.StatusCode)
	}
}

func isDiscord(target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return (h == "discord.com" || h == "discordapp.com" || strings.HasSuffix(h, ".discord.com")) && strings.HasPrefix(u.Path, "/api/webhooks/")
}

// host keeps webhook tokens, which live in the URL path, out of the log.
func host(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return "?"
	}
	return u.Host
}

func redact(msg, target string) string {
	return strings.ReplaceAll(msg, target, host(target))
}

// AddedSummary is the one-line list for an added notice: up to five titles
// and a count of the rest.
func AddedSummary(titles []string) string {
	const shown = 5
	if len(titles) <= shown {
		return strings.Join(titles, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(titles[:shown], ", "), len(titles)-shown)
}
