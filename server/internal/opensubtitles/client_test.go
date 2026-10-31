package opensubtitles_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles/ostest"
)

func TestSearchSendsSortedParamsAndReadsResults(t *testing.T) {
	fake := ostest.New(t)
	c := fake.Client("", "")
	res, err := c.Search(context.Background(), opensubtitles.Query{IMDbID: 1234567, Type: "movie", Languages: []string{"FR", "en"}})
	if err != nil {
		t.Fatal(err)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	// Sorted and lowercase, or the API answers with a redirect first.
	if r.RawQuery != "ai_translated=exclude&imdb_id=1234567&languages=en%2Cfr&machine_translated=exclude&type=movie" {
		t.Errorf("query %q", r.RawQuery)
	}
	if r.APIKey != "fixture-api-key" || r.UserAgent != "VCRX vtest" || r.Auth != "" {
		t.Errorf("headers: key %q, agent %q, auth %q", r.APIKey, r.UserAgent, r.Auth)
	}
	// Five entries in the fixture; the two-CD one cannot be one track.
	if len(res) != 4 {
		t.Fatalf("%d results: %+v", len(res), res)
	}
	byID := map[int64]opensubtitles.Result{}
	for _, x := range res {
		byID[x.FileID] = x
	}
	if !byID[8000003].Trusted || !byID[8000003].HearingImpaired || byID[8000003].DownloadCount != 1500 {
		t.Errorf("trusted SDH entry: %+v", byID[8000003])
	}
	if !byID[8000004].Trusted {
		t.Error("an administrator's upload should count as trusted")
	}
	if byID[8000002].Trusted || byID[8000002].Language != "en" || byID[8000002].Release != "The.Film.2001.720p.WEB" {
		t.Errorf("plain entry: %+v", byID[8000002])
	}
	if _, ok := byID[8000005]; ok {
		t.Error("multi-file subtitle was kept")
	}
}

func TestDownloadLogsInFetchesAndTracksQuota(t *testing.T) {
	fake := ostest.New(t)
	c := fake.Client("viewer", "secret")
	f, err := c.Download(context.Background(), 8000002)
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Content) != ostest.Content || f.Name != "The.Film.2001.1080p.BluRay.x264-GROUP.srt" {
		t.Fatalf("file %q %q", f.Name, f.Content)
	}
	if !f.Quota.Known || f.Quota.Remaining != 19 || f.Quota.ResetAt.IsZero() {
		t.Fatalf("quota %+v", f.Quota)
	}
	var sawDownload, sawFile bool
	for _, r := range fake.Requests() {
		switch {
		case r.Path == "/api/v1/download":
			sawDownload = true
			if r.Auth != "Bearer fixture-token-1" {
				t.Errorf("download auth %q", r.Auth)
			}
		case strings.HasPrefix(r.Path, "/files/"):
			sawFile = true
			// The link is another host in real life: no key, no token.
			if r.APIKey != "" || r.Auth != "" {
				t.Errorf("credentials sent to the download link: key %q auth %q", r.APIKey, r.Auth)
			}
		}
	}
	if !sawDownload || !sawFile || fake.LoginCount != 1 {
		t.Fatalf("download %v, file %v, logins %d", sawDownload, sawFile, fake.LoginCount)
	}
	// The token is reused.
	if _, err := c.Download(context.Background(), 8000003); err != nil || fake.LoginCount != 1 {
		t.Fatalf("second download: %v, logins %d", err, fake.LoginCount)
	}
}

func TestDownloadQuotaExhausted(t *testing.T) {
	fake := ostest.New(t)
	fake.Remaining = 0
	c := fake.Client("viewer", "secret")
	_, err := c.Download(context.Background(), 8000002)
	var qe *opensubtitles.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("err %v, want a QuotaError", err)
	}
	want := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if !qe.Quota.ResetAt.Equal(want) || qe.Quota.Remaining != 0 {
		t.Fatalf("quota %+v, want reset at %s", qe.Quota, want)
	}
	if q := c.LastQuota(); !q.Known || q.Remaining != 0 {
		t.Fatalf("last quota %+v", q)
	}
}

func TestExpiredTokenLogsInAgainOnce(t *testing.T) {
	fake := ostest.New(t)
	fake.ExpireFirstToken = true
	c := fake.Client("viewer", "secret")
	if _, err := c.Download(context.Background(), 8000002); err != nil {
		t.Fatal(err)
	}
	if fake.LoginCount != 2 {
		t.Fatalf("logins %d, want 2", fake.LoginCount)
	}
}

func TestDownloadNeedsAnAccount(t *testing.T) {
	fake := ostest.New(t)
	c := fake.Client("", "")
	if _, err := c.Download(context.Background(), 1); !errors.Is(err, opensubtitles.ErrNoAccount) {
		t.Fatalf("err %v", err)
	}
	if len(fake.Requests()) != 0 {
		t.Fatal("a request was sent without an account")
	}
	bad := fake.Client("viewer", "wrong")
	if _, err := bad.Download(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestRateLimitBacksOffWithoutCalling(t *testing.T) {
	fake := ostest.New(t)
	fake.RateLimit = true
	c := fake.Client("", "")
	_, err := c.Search(context.Background(), opensubtitles.Query{Title: "x", Languages: []string{"en"}})
	var rl *opensubtitles.RateLimitError
	if !errors.As(err, &rl) || time.Until(rl.Until) < 25*time.Second {
		t.Fatalf("err %v, want a 30 s RateLimitError", err)
	}
	fake.RateLimit = false
	if _, err := c.Search(context.Background(), opensubtitles.Query{Title: "x"}); !errors.As(err, &rl) {
		t.Fatalf("second search: %v, want refused locally", err)
	}
	if n := len(fake.Requests()); n != 1 {
		t.Fatalf("%d requests; the client should not ask again while blocked", n)
	}
}

func TestOfflineIsUnavailable(t *testing.T) {
	fake := ostest.New(t)
	c := fake.Client("viewer", "secret")
	fake.Close()
	if _, err := c.Search(context.Background(), opensubtitles.Query{Title: "x"}); !errors.Is(err, opensubtitles.ErrUnavailable) {
		t.Fatalf("search: %v", err)
	}
	if _, err := c.Download(context.Background(), 1); !errors.Is(err, opensubtitles.ErrUnavailable) {
		t.Fatalf("download: %v", err)
	}
	down := ostest.New(t)
	down.Down = true
	if _, err := down.Client("", "").Search(context.Background(), opensubtitles.Query{Title: "x"}); !errors.Is(err, opensubtitles.ErrUnavailable) {
		t.Fatalf("503: %v", err)
	}
}

func TestOffWithoutKey(t *testing.T) {
	if c := opensubtitles.New("  ", "u", "p", ""); c != nil {
		t.Fatal("a blank key should leave the client off")
	}
	var c *opensubtitles.Client
	if _, err := c.Search(context.Background(), opensubtitles.Query{}); !errors.Is(err, opensubtitles.ErrNotConfigured) {
		t.Fatalf("err %v", err)
	}
}
