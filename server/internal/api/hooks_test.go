package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/arr"
)

func TestArrWebhook(t *testing.T) {
	f := newFixture(t)
	body := `{"eventType":"Download","movie":{"id":7,"title":"The Film","folderPath":"/volume1/movies/Film 2001 1080p","year":2001},
		"movieFile":{"relativePath":"film.1080p.mkv","quality":"Bluray-1080p"},"isUpgrade":false,"instanceName":"Radarr"}`
	call := func(target, ip string, hdr ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", target, strings.NewReader(body))
		req.RemoteAddr = ip
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, req)
		return w
	}
	const lan, token = "192.168.1.40:1", "a-long-shared-token-123"
	if w := call("/api/v1/hooks/arr?token="+token, lan); w.Code != 404 {
		t.Fatalf("off: %d", w.Code)
	}
	var scans []string
	f.srv.ArrToken = token
	f.srv.Arr = &arr.Dispatcher{
		Libraries: func(context.Context) ([]arr.Library, error) {
			return []arr.Library{{ID: 1, Kind: "movie", RootPath: f.root}}, nil
		},
		Scan: func(_ context.Context, lib int64, sub string) (int64, error) {
			scans = append(scans, sub)
			return 42, nil
		},
	}
	if _, err := os.Stat(filepath.Join(f.root, "Film 2001 1080p")); err != nil {
		t.Fatal(err)
	}
	if w := call("/api/v1/hooks/arr?token="+token, "8.8.8.8:1"); w.Code != 403 {
		t.Fatalf("from the internet: %d", w.Code)
	}
	if w := call("/api/v1/hooks/arr?token=wrong-token-xxxxxxxx", lan); w.Code != 401 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if w := call("/api/v1/hooks/arr", lan); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	w := call("/api/v1/hooks/arr?token="+token, lan)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"action":"scanned"`) || len(scans) != 1 || scans[0] != "Film 2001 1080p" {
		t.Fatalf("download: %d %s %v", w.Code, w.Body.String(), scans)
	}
	// Radarr's basic-auth password and the header work too.
	req := "/api/v1/hooks/arr"
	if w := call(req, lan, "Authorization", "Basic "+basic("radarr", token)); w.Code != 202 {
		t.Fatalf("basic auth: %d %s", w.Code, w.Body.String())
	}
	if w := call(req, lan, "X-VCRX-Token", token); w.Code != 202 {
		t.Fatalf("header: %d", w.Code)
	}
}

func basic(user, pass string) string {
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth(user, pass)
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
}
