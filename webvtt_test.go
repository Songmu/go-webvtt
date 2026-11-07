package webvtt

import (
	"encoding/json"
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
