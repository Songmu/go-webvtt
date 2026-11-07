package webvtt

import (
	"bytes"
	"strings"
	"testing"
)

func TestDocumentLossless(t *testing.T) {
	for _, newline := range []string{"\n", "\r", "\r\n"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(newline, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			raw := []byte(strings.Join([]string{
				"\ufeffWEBVTT description", "metadata", "",
				"NOTE comment", "untouched", "", "STYLE", "::cue {color:red}", "",
				"REGION", "id:main", "", "cue-id",
				"100:00:01.000 --> 100:00:02.000 align:start",
				"<v Alice><b>日本</b>&amp;語</v> after", "second", "",
			}, newline))
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
	inputs := []string{
		"WEBVTTx\n\n00:01.000 --> 00:02.000\ntext",
		"WEBVTT -->\n",
		"WEBVTT\n00:01.000 --> 00:02.000\ntext",
		"WEBVTT\n\n00:01.000 --> 00:02.000\n\xff",
		"WEBVTT\n\n00:01.000 --> 00:02.000\n\x00",
		"WEBVTT\n\ncue-id",
	}
	for _, ts := range []string{
		"1:01.000", "00:60.000", "60:00.000", "00:01", "00:01.00",
		"00:01.000junk", "-1:00:01.000", "01:00:01.000.1",
		"99999999999999999999:00:01.000",
	} {
		inputs = append(inputs, "WEBVTT\n\n"+ts+" --> 00:02.000\ntext")
	}
	inputs = append(inputs,
		"WEBVTT\n\n00:02.000 --> 00:01.000\ntext",
		"WEBVTT\n\n00:01.000 --> 00:01.000\ntext",
		"WEBVTT\n\n00:01.000-->00:02.000\ntext")
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseDocument([]byte(input)); err == nil {
				t.Fatal("expected lossless error")
			}
			// The legacy iterator intentionally retains extended MM:SS support.
			if _, err := ParseAll(strings.NewReader(input)); err == nil && !strings.Contains(input, "60:00.000") {
				t.Fatal("expected legacy error")
			}
		})
	}
}

func TestDocumentPatchSafety(t *testing.T) {
	raw := []byte("WEBVTT\n\n00:01.000 --> 00:02.000\n<v A>日本&amp;語</v>\n")
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
	raw := []byte("WEBVTT\n\n00:01.000 --> 00:02.000\n<v A>x&amp;<i>y</i></v>\nz\n")
	d, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}

	nodes := d.TextNodes
	text, err := d.CueText([]Patch{{nodes[0].Start, nodes[0].End, ""}, {nodes[1].Start, nodes[1].End, ""}})
	if err != nil || len(text) != 1 || text[0] != "&\nz" {
		t.Fatalf("visible text: %q, %v", text, err)
	}
	d, err = ParseDocument([]byte("WEBVTT\n\n00:01.000 --> 00:02.000\nfirst\nsecond\n"))
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
		payload string
		valid   bool
	}{
		{"a<00:01.500>b", true},
		{"a<00:99.000>b", false},
		{"a<00:01.000>b", false},
		{"a<00:02.000>b", false},
		{"a<00:01.500>b<00:01.400>c", false},
	} {
		d, err := ParseDocument([]byte("WEBVTT\n\n00:01.000 --> 00:02.000\n" + tc.payload))
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.payload, err)
		}
		if tc.valid {
			text, err := d.CueText(nil)
			if err != nil || text[0] != "ab" {
				t.Fatalf("timestamp must be read-only: %v, %v", text, err)
			}
		}
	}
}

func TestDocumentRejectsEntityIntroduction(t *testing.T) {
	raw := []byte("WEBVTT\n\n00:01.000 --> 00:02.000\n& word;")
	d, err := ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(raw, []byte(" word"))
	if _, err := d.Apply([]Patch{{start, start + len(" word"), "amp"}}); err == nil {
		t.Fatal("accepted character reference assembled from source and replacement")
	}
}
