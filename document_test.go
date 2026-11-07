package webvtt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDocumentFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "document", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDocumentLossless(t *testing.T) {
	fixture := readDocumentFixture(t, "lossless.vtt")
	for _, newline := range []string{"\n", "\r", "\r\n"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(newline, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			raw := bytes.ReplaceAll(fixture, []byte("\n"), []byte(newline))
			d, err := ParseDocument(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.Apply(nil)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("round trip: %q, %v", got, err)
			}
			if len(d.Blocks) != 4 || len(d.TextNodes) != 4 {
				t.Fatalf("blocks/nodes: %+v / %+v", d.Blocks, d.TextNodes)
			}
			node := d.TextNodes[0]
			if string(raw[node.Start:node.End]) != "日本" || node.Speaker != "Alice" || node.Cue != 0 {
				t.Fatalf("node: %+v", node)
			}
			got, err = d.Apply([]Patch{{node.Start, node.End, "英語"}})
			if err != nil || !bytes.Equal(got, bytes.Replace(raw, []byte("日本"), []byte("英語"), 1)) {
				t.Fatalf("patch: %q, %v", got, err)
			}
			var blocks []Block
			for block, err := range Parse(bytes.NewReader(raw)) {
				if err != nil {
					t.Fatal(err)
				}
				blocks = append(blocks, block)
			}
			if len(blocks) != 4 {
				t.Fatalf("legacy blocks: %d", len(blocks))
			}
		})
	}
}

func TestDocumentLongAndMixedLines(t *testing.T) {
	raw := []byte("WEBVTT\r\n\r00:01.000 --> 00:02.000\n" + strings.Repeat("あ", 50000))
	d, err := ParseDocument(raw)
	if err != nil || len(d.TextNodes) != 1 {
		t.Fatalf("long document: %v", err)
	}
	if _, err := ParseAll(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	got, err := d.Apply(nil)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatalf("mixed round trip: %v", err)
	}
}

func TestDocumentRejectsMalformed(t *testing.T) {
	for _, name := range collectVTTFiles(t, "document/invalid-*.vtt") {
		t.Run(name, func(t *testing.T) {
			raw := readDocumentFixture(t, name)
			if _, err := ParseDocument(raw); err == nil {
				t.Fatal("expected lossless error")
			}
			// The legacy iterator intentionally retains extended MM:SS support.
			if _, err := ParseAll(bytes.NewReader(raw)); err == nil && name != "invalid-extended-minutes.vtt" {
				t.Fatal("expected legacy error")
			}
		})
	}
}

func TestDocumentTimestampErrorPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, timing, want string
	}{
		{"strict-range", "60:00.000 --> 61:00.000", "timestamp minutes and seconds must be below 60"},
		{"invalid-end", "60:00.000 --> invalid", "invalid end timestamp: invalid timestamp format"},
		{"missing-end", "60:00.000 -->", "missing end timestamp"},
		{"reversed-times", "60:00.000 --> 00:02.000", "end timestamp must be greater than start timestamp"},
		{"arrow-spacing", "60:00.000-->61:00.000", "timestamp arrow must be surrounded by whitespace"},
		{"strict-end-range", "00:01.000 --> 60:00.000", "timestamp minutes and seconds must be below 60"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDocument([]byte("WEBVTT\n\n" + tc.timing + "\ntext\n"))
			want := "block at byte 8: " + tc.want
			if err == nil || err.Error() != want {
				t.Fatalf("parse error: %v, want %q", err, want)
			}
		})
	}
}

func TestDocumentRejectsInvalidBytes(t *testing.T) {
	fixture := readDocumentFixture(t, "multiline.vtt")
	for _, tc := range []struct {
		name string
		b    byte
	}{
		{"invalid-utf8", 0xff},
		{"nul", 0x00},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := append(bytes.Clone(fixture), tc.b)
			if _, err := ParseDocument(raw); err == nil {
				t.Fatal("expected lossless error")
			}
			if _, err := ParseAll(bytes.NewReader(raw)); err == nil {
				t.Fatal("expected legacy error")
			}
		})
	}
}

func TestDocumentPatchSafety(t *testing.T) {
	raw := readDocumentFixture(t, "patch-safety.vtt")
	d, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := d.TextNodes[0]
	for _, patches := range [][]Patch{
		{{-1, 1, "x"}}, {{n.Start + 1, n.End, "x"}}, {{0, 1, "x"}},
		{{n.Start, n.End + 1, "x"}}, {{n.End, n.End + 5, "x"}},
		{{n.Start, n.End, "\xff"}}, {{n.Start, n.End, "<b>"}},
		{{n.Start, n.End, "&amp;"}}, {{n.Start, n.End, "\n"}},
		{{n.Start, n.End, "\x00"}}, {{n.Start, n.End, "x"}, {n.Start, n.Start, "y"}},
		{{n.Start, n.Start, "x"}, {n.Start, n.Start, "y"}},
	} {
		if _, err := d.Apply(patches); err == nil {
			t.Fatalf("accepted invalid patches: %+v", patches)
		}
	}
	d.Raw[0] = 'X'
	d.TextNodes[0].Start = 0
	got, err := d.Apply([]Patch{{n.Start, n.Start, "新"}})
	if err != nil || !bytes.Equal(got, bytes.Replace(raw, []byte("日本"), []byte("新日本"), 1)) {
		t.Fatalf("snapshot/insertion: %q, %v", got, err)
	}
}

func TestDocumentVisibleTextAndStructure(t *testing.T) {
	raw := readDocumentFixture(t, "visible-text.vtt")
	d, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}

	nodes := d.TextNodes
	text, err := d.CueText([]Patch{{nodes[0].Start, nodes[0].End, ""}, {nodes[1].Start, nodes[1].End, ""}})
	if err != nil || len(text) != 1 || text[0] != "&\nz" {
		t.Fatalf("visible text: %q, %v", text, err)
	}
	d, err = ParseDocument(readDocumentFixture(t, "multiline.vtt"))
	if err != nil {
		t.Fatal(err)
	}
	n := d.TextNodes[0]
	if _, err := d.Apply([]Patch{{n.Start, n.End, ""}}); err == nil {
		t.Fatal("accepted new blank separator in middle of payload")
	}
}

func TestDocumentInlineTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"inline-timestamp.vtt", true},
		{"invalid-inline-timestamp.vtt", false},
		{"invalid-inline-at-start.vtt", false},
		{"invalid-inline-at-end.vtt", false},
		{"invalid-inline-order.vtt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseDocument(readDocumentFixture(t, filepath.Join("inline-timestamps", tc.name)))
			if (err == nil) != tc.valid {
				t.Fatalf("parse: %v", err)
			}
			if tc.valid {
				text, err := d.CueText(nil)
				if err != nil || text[0] != "ab" {
					t.Fatalf("timestamp must be read-only: %v, %v", text, err)
				}
			}
		})
	}
}

func TestDocumentTextScannerCrossLineState(t *testing.T) {
	raw := []byte("WEBVTT\r\n\r\n00:01.000 --> 00:02.000\r\n" +
		"<v.outer \tOuter  Voice \t>outer< v.inner\tInner  Voice >inner<unfinished\r\n" +
		"continued</v>restored</v>after&Unknown9;tail&; &#; &#x41; &unfinished")
	d, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		text, speaker string
	}{
		{"outer", "Outer  Voice"},
		{"inner", "r\tInner  Voice"},
		{"continued", "r\tInner  Voice"},
		{"restored", "Outer  Voice"},
		{"after", ""},
		{"tail&; ", ""},
		{" ", ""},
		{" &unfinished", ""},
	}
	if len(d.TextNodes) != len(want) {
		t.Fatalf("nodes: %+v", d.TextNodes)
	}
	for i, expected := range want {
		node := d.TextNodes[i]
		text := string(raw[node.Start:node.End])
		if text != expected.text || node.Speaker != expected.speaker || node.Cue != 0 {
			t.Fatalf("node %d: %q, %+v; want %q and speaker %q",
				i, text, node, expected.text, expected.speaker)
		}
	}
}

func TestDocumentInlineTimestampCrossLineOrder(t *testing.T) {
	for _, tc := range []struct {
		tag   string
		valid bool
	}{
		{"00:01.600", true},
		{"00:01.500", false},
		{"00:01.400", false},
		{"00:broken", false},
		{" 00:broken", true},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			raw := []byte("WEBVTT\n\n00:01.000 --> 00:02.000\n" +
				"first<00:01.500>\nsecond<" + tc.tag + ">third")
			_, err := ParseDocument(raw)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			want := "cue at byte 8: invalid inline timestamp <" + tc.tag + ">"
			if err == nil || err.Error() != want {
				t.Fatalf("error: %v, want %q", err, want)
			}
		})
	}
}

func TestDocumentRejectsEntityIntroduction(t *testing.T) {
	raw := readDocumentFixture(t, "entity-introduction.vtt")
	d, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(raw, []byte(" word"))
	if _, err := d.Apply([]Patch{{start, start + len(" word"), "amp"}}); err == nil {
		t.Fatal("accepted character reference assembled from source and replacement")
	}
}
