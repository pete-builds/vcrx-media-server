package opensubtitles

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Stub answers like OpenSubtitles without touching the network. The dev
// server and the browser smoke test use it (VCRX_OPENSUBTITLES_STUB=1), so CI
// never calls the real API and needs no key.
type Stub struct {
	mu        sync.Mutex
	remaining int
}

func NewStub() *Stub { return &Stub{remaining: 20} }

func (s *Stub) CanDownload() bool { return true }

// Search returns two invented files per language: a hash match when the
// query carries a hash, and a hearing-impaired one that is not.
func (s *Stub) Search(_ context.Context, q Query) ([]Result, error) {
	langs := q.Languages
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	var out []Result
	for i, l := range langs {
		base := int64(9_000_000 + 10*i)
		out = append(out,
			Result{FileID: base + 1, FileName: "Stub.Release.1080p." + l + ".srt", Language: l, Release: "Stub.Release.1080p.WEB",
				HashMatch: q.MovieHash != "", Trusted: true, DownloadCount: 1200, Uploader: "stub"},
			Result{FileID: base + 2, FileName: "Stub.Release.720p.SDH." + l + ".srt", Language: l, Release: "Stub.Release.720p.HDTV",
				HearingImpaired: true, DownloadCount: 300, Uploader: "stub"})
	}
	return out, nil
}

func (s *Stub) Download(_ context.Context, fileID int64) (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remaining <= 0 {
		return File{}, &QuotaError{Quota: s.quota()}
	}
	s.remaining--
	text := strings.Join([]string{
		"1", "00:00:02,000 --> 00:00:05,000", fmt.Sprintf("Downloaded subtitle %d", fileID), "",
		"2", "00:00:06,000 --> 00:00:10,000", "Second downloaded line", "",
	}, "\n")
	return File{Name: fmt.Sprintf("stub.%d.srt", fileID), Content: []byte(text), Quota: s.quota()}, nil
}

func (s *Stub) quota() Quota {
	return Quota{Known: true, Remaining: s.remaining, Allowed: 20, Level: "stub", CheckedAt: time.Now()}
}

func (s *Stub) LastQuota() Quota {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quota()
}

func (s *Stub) Quota(context.Context) (Quota, error) { return s.LastQuota(), nil }
