package api

import (
	"math/rand/v2"
	"net/http"
	"strconv"
)

// Speed test sizes. The cap keeps one request from tying up the uplink for long;
// a client that wants a longer test asks again.
const (
	speedTestDefault = 8_000_000
	speedTestMax     = 64_000_000
)

// speedBlock is what the speed test sends, repeated. It is random so a proxy
// that compresses cannot make the link look faster than it is.
var speedBlock = func() []byte {
	b := make([]byte, 64<<10)
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // filler bytes, not a secret
	for i := range b {
		b[i] = byte(r.UintN(256))
	}
	return b
}()

// speedTest sends ?bytes= of filler (default 8 MB, at most 64 MB) for a client
// to time its connection to the server. It reads nothing from the library.
func (s *Server) speedTest(w http.ResponseWriter, r *http.Request) {
	n := int64(speedTestDefault)
	if v := r.URL.Query().Get("bytes"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil || parsed < 1 || parsed > speedTestMax {
			writeError(w, http.StatusBadRequest, "bad_request", "bytes must be between 1 and "+strconv.Itoa(speedTestMax)+".")
			return
		}
		n = parsed
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	w.Header().Set("Cache-Control", "no-store")
	for n > 0 {
		chunk := speedBlock
		if int64(len(chunk)) > n {
			chunk = chunk[:n]
		}
		if _, err := w.Write(chunk); err != nil {
			return // the client went away
		}
		n -= int64(len(chunk))
	}
}
