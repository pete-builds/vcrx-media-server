package markers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
)

// Decoder reads what detection needs out of a media file with ffmpeg and
// ffprobe, each at the lowest CPU priority (nice 19), one at a time.
type Decoder struct {
	FFmpeg, FFprobe string
}

// PCM decodes lenMs of the first audio track from startMs as mono 16 bit
// samples at SampleRate.
func (d Decoder) PCM(ctx context.Context, path string, startMs, lenMs int64) ([]int16, error) {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if startMs > 0 {
		args = append(args, "-ss", seconds(startMs))
	}
	args = append(args, "-t", seconds(lenMs), "-i", path, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", strconv.Itoa(SampleRate), "-f", "s16le", "pipe:1")
	out, _, err := d.run(ctx, d.FFmpeg, args)
	if err != nil {
		return nil, err
	}
	samples := make([]int16, len(out)/2)
	if err := binary.Read(bytes.NewReader(out[:2*len(samples)]), binary.LittleEndian, samples); err != nil {
		return nil, err
	}
	return samples, nil
}

// Duration is the file's length from its container.
func (d Decoder) Duration(ctx context.Context, path string) (int64, error) {
	out, _, err := d.run(ctx, d.FFprobe, []string{"-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path})
	if err != nil {
		return 0, err
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration %q: %w", strings.TrimSpace(string(out)), err)
	}
	return int64(secs * 1000), nil
}

// Chapter is a named chapter of a file.
type Chapter struct {
	Title          string
	StartMs, EndMs int64
}

// Chapters lists the file's chapters.
func (d Decoder) Chapters(ctx context.Context, path string) ([]Chapter, error) {
	out, _, err := d.run(ctx, d.FFprobe, []string{"-v", "error", "-show_chapters", "-of", "json", path})
	if err != nil {
		return nil, err
	}
	var doc struct {
		Chapters []struct {
			Start string            `json:"start_time"`
			End   string            `json:"end_time"`
			Tags  map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("ffprobe chapters: %w", err)
	}
	var cs []Chapter
	for _, c := range doc.Chapters {
		s, err1 := strconv.ParseFloat(c.Start, 64)
		e, err2 := strconv.ParseFloat(c.End, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		title := c.Tags["title"]
		if title == "" {
			title = c.Tags["TITLE"]
		}
		cs = append(cs, Chapter{Title: title, StartMs: int64(s * 1000), EndMs: int64(e * 1000)})
	}
	return cs, nil
}

var blackRe = regexp.MustCompile(`black_start:\s*([0-9.]+)\s+black_end:\s*([0-9.]+)`)

// BlackFrom returns where the picture goes black for good, judged from the
// keyframes of the stretch from startMs to the end, or -1 when it does not.
// Decoding keyframes only keeps this cheap; it is used only when the end
// credits could not be matched.
func (d Decoder) BlackFrom(ctx context.Context, path string, startMs, durationMs int64) (int64, error) {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "info", "-skip_frame", "nokey", "-ss", seconds(startMs),
		"-i", path, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", "scale=64:-2,blackdetect=d=1:pix_th=0.10", "-f", "null", "-"}
	_, log, err := d.run(ctx, d.FFmpeg, args)
	if err != nil {
		return -1, err
	}
	from := int64(-1)
	for _, m := range blackRe.FindAllStringSubmatch(string(log), -1) {
		s, _ := strconv.ParseFloat(m[1], 64)
		e, _ := strconv.ParseFloat(m[2], 64)
		end := startMs + int64(e*1000)
		// Keyframes can sit a few seconds apart, so "reaches the end" is loose.
		if durationMs-end <= 3000 {
			from = startMs + int64(s*1000)
		}
	}
	return from, nil
}

// run starts a tool at low priority and returns its stdout and stderr.
func (d Decoder) run(ctx context.Context, tool string, args []string) (stdoutB, stderrB []byte, err error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	lowPriority(cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, nil, fmt.Errorf("%s: %w: %s", filepath.Base(tool), err, msg)
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func seconds(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }

// fromChapters picks the intro and credits out of chapter titles. Only clear
// names count: a chapter called "Chapter 2" says nothing.
func fromChapters(cs []Chapter) map[string]Marker {
	out := map[string]Marker{}
	for _, c := range cs {
		if c.EndMs <= c.StartMs {
			continue
		}
		kind := ""
		switch strings.Join(strings.Fields(strings.ToLower(c.Title)), " ") {
		case "intro", "opening", "opening credits", "opening titles", "title sequence", "theme", "opening theme", "op":
			kind = Intro
		case "credits", "end credits", "ending", "ending credits", "closing credits", "outro", "end titles", "ed":
			kind = Credits
		}
		if _, dup := out[kind]; kind != "" && !dup {
			out[kind] = Marker{Kind: kind, StartMs: c.StartMs, EndMs: c.EndMs, Confidence: 1, Source: SourceChapter}
		}
	}
	return out
}

// fromNFO reads Plex-style markers from an episode's NFO, beside the file
// with the same name: <Marker type="intro" startTimeOffset="0"
// endTimeOffset="51000"/>, times in milliseconds, as Plex's own API writes
// them. Anything else in the file is ignored, and a missing file is no error.
// It is read only from inside root and only as a regular file, like every other
// file the server opens from a library.
func fromNFO(root, mediaPath string) (map[string]Marker, error) {
	p := strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath)) + ".nfo"
	f, _, err := fsafe.OpenUnder(root, p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]Marker{}
	dec := xml.NewDecoder(io.LimitReader(f, 1<<20))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			// A broken NFO is the metadata reader's problem; keep what was read.
			return out, nil //nolint:nilerr // see above
		}
		el, ok := tok.(xml.StartElement)
		if !ok || !strings.EqualFold(el.Name.Local, "marker") {
			continue
		}
		var kind string
		start, end := int64(-1), int64(-1)
		for _, a := range el.Attr {
			switch strings.ToLower(a.Name.Local) {
			case "type":
				kind = strings.ToLower(strings.TrimSpace(a.Value))
			case "starttimeoffset":
				start, _ = strconv.ParseInt(strings.TrimSpace(a.Value), 10, 64)
			case "endtimeoffset":
				end, _ = strconv.ParseInt(strings.TrimSpace(a.Value), 10, 64)
			}
		}
		if _, dup := out[kind]; ValidKind(kind) && !dup && start >= 0 && end > start {
			out[kind] = Marker{Kind: kind, StartMs: start, EndMs: end, Confidence: 1, Source: SourceNFO}
		}
	}
}
