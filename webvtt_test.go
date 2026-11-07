package webvtt

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tenntenn/golden"
)

func collectVTTFiles(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("testdata", pattern))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}
	return names
}

func TestParseAll(t *testing.T) {
	// Valid VTT files: testdata/*.vtt excluding invalid-*.vtt
	allFiles := collectVTTFiles(t, "*.vtt")
	for _, name := range allFiles {
		if strings.HasPrefix(name, "invalid-") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			vtt, err := ParseAll(f)
			if err != nil {
				t.Fatal(err)
			}

			got, err := json.MarshalIndent(vtt, "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			if os.Getenv("UPDATE_GOLDEN") != "" {
				golden.Update(t, "testdata", name, got)
				return
			}
			if diff := golden.Diff(t, "testdata", name, got); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func TestParse(t *testing.T) {
	// Files that contain non-cue blocks (NOTE, STYLE, REGION)
	blockFiles := []string{
		"note-blocks.vtt",
		"style-region.vtt",
	}

	for _, name := range blockFiles {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			var blocks []Block
			for block, err := range Parse(f) {
				if err != nil {
					t.Fatal(err)
				}
				blocks = append(blocks, block)
			}

			got, err := json.MarshalIndent(blocks, "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			goldenName := name + ".blocks"
			if os.Getenv("UPDATE_GOLDEN") != "" {
				golden.Update(t, "testdata", goldenName, got)
				return
			}
			if diff := golden.Diff(t, "testdata", goldenName, got); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func TestParseRegionSettings(t *testing.T) {
	for _, first := range []string{"REGION", "REGION width: 10%", "REGION\tid: inline"} {
		t.Run(first, func(t *testing.T) {
			block, err := parseBlock([]string{
				first, "REGION", " id : first ", "id: last", " width : 20% ", "width: 30%", "ignored", ":ignored",
			})
			want := Region{ID: "last", Settings: map[string]string{"width": "30%"}}
			if first != "REGION" {
				key, value, _ := strings.Cut(first, ":")
				want.Settings[key] = strings.TrimSpace(value)
			}
			if err != nil || !reflect.DeepEqual(block, want) {
				t.Fatalf("region: %#v, %v, want %#v", block, err, want)
			}
		})
	}
}

func TestParseAll_Invalid(t *testing.T) {
	// Invalid VTT files: testdata/invalid-*.vtt
	invalidFiles := collectVTTFiles(t, "invalid-*.vtt")
	for _, name := range invalidFiles {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			_, err = ParseAll(f)
			if err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

type failingReader struct {
	err   error
	reads int
}

func (r *failingReader) Read([]byte) (int, error) {
	r.reads++
	return 0, r.err
}

func TestParseCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		wantError bool
		wantReads int
	}{
		{"block", "WEBVTT\n\n00:01.000 --> 00:02.000\ntext\n\n", false, 0},
		{"block-error", "WEBVTT\n\nbroken\n\n", true, 0},
		{"last-block", "WEBVTT\n\n00:01.000 --> 00:02.000\ntext", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tail := &failingReader{err: errors.New("reader failure")}
			calls := 0
			Parse(io.MultiReader(strings.NewReader(tc.input), tail))(func(block Block, err error) bool {
				calls++
				if (err != nil) != tc.wantError || (block == nil) != tc.wantError {
					t.Fatalf("yield: %v, %v", block, err)
				}
				return false
			})
			if calls != 1 || tail.reads != tc.wantReads {
				t.Fatalf("calls/reads: %d/%d, want 1/%d", calls, tail.reads, tc.wantReads)
			}
		})
	}
}

func TestParseReaderErrors(t *testing.T) {
	failure := errors.New("reader failure")
	for _, tc := range []struct {
		name       string
		input      string
		wantBlocks int
		wantError  string
	}{
		{"header", "", 0, failure.Error()},
		{"after-block", "WEBVTT\n\n00:01.000 --> 00:02.000\ntext\n\n", 1, failure.Error()},
		{"after-last-block", "WEBVTT\n\n00:01.000 --> 00:02.000\ntext", 1, failure.Error()},
		// Characterize legacy behavior: a final parse error takes precedence over a reader error.
		{"legacy-final-parse-error-priority", "WEBVTT\n\nbroken", 0, "missing timestamp line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := io.MultiReader(strings.NewReader(tc.input), &failingReader{err: failure})
			var blocks []Block
			var errs []error
			for block, err := range Parse(reader) {
				if err != nil {
					if len(blocks) != tc.wantBlocks {
						t.Fatalf("error yielded before blocks: %v", err)
					}
					errs = append(errs, err)
				} else {
					blocks = append(blocks, block)
				}
			}
			if len(blocks) != tc.wantBlocks || len(errs) != 1 || errs[0].Error() != tc.wantError {
				t.Fatalf("blocks/errors: %v/%v, want %d/%q", blocks, errs, tc.wantBlocks, tc.wantError)
			}
			if tc.wantError == failure.Error() && !errors.Is(errs[0], failure) {
				t.Fatalf("reader error identity lost: %v", errs[0])
			}
		})
	}
}

func TestParseContinuesAfterBlockError(t *testing.T) {
	input := "WEBVTT\n\nbroken\n\n00:01.000 --> 00:02.000\ntext\n"
	calls := 0
	for block, err := range Parse(strings.NewReader(input)) {
		switch calls {
		case 0:
			if block != nil || err == nil || err.Error() != "missing timestamp line" {
				t.Fatalf("first yield: %v, %v", block, err)
			}
		case 1:
			cue, ok := block.(Cue)
			if err != nil || !ok || len(cue.Voices) != 1 || cue.Voices[0].Text != "text" {
				t.Fatalf("second yield: %v, %v", block, err)
			}
		default:
			t.Fatalf("unexpected yield: %v, %v", block, err)
		}
		calls++
	}
	if calls != 2 {
		t.Fatalf("yield count: %d, want 2", calls)
	}
}
