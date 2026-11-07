go-webvtt
=======

[![Test Status](https://github.com/Songmu/go-webvtt/actions/workflows/test.yaml/badge.svg?branch=main)][actions]
[![Coverage Status](https://codecov.io/gh/Songmu/go-webvtt/branch/main/graph/badge.svg)][codecov]
[![MIT License](https://img.shields.io/github/license/Songmu/go-webvtt)][license]
[![PkgGoDev](https://pkg.go.dev/badge/github.com/Songmu/go-webvtt)][PkgGoDev]

[actions]: https://github.com/Songmu/go-webvtt/actions?workflow=test
[codecov]: https://codecov.io/gh/Songmu/go-webvtt
[license]: https://github.com/Songmu/go-webvtt/blob/main/LICENSE
[PkgGoDev]: https://pkg.go.dev/github.com/Songmu/go-webvtt

A Go library for parsing WebVTT (Web Video Text Tracks) files.

## Synopsis

```go
package main

import (
	"fmt"
	"os"

	"github.com/Songmu/go-webvtt"
)

func main() {
	f, _ := os.Open("subtitles.vtt")
	defer f.Close()

	// Batch parsing - get all cues at once
	vtt, _ := webvtt.ParseAll(f)
	for _, cue := range vtt.Cues {
		fmt.Printf("[%v - %v] %s\n", cue.StartTime, cue.EndTime, cue.Voices[0].Text)
	}
}
```

```go
// Stream parsing - process blocks one by one
f, _ := os.Open("subtitles.vtt")
defer f.Close()

for block, err := range webvtt.Parse(f) {
	if err != nil {
		log.Fatal(err)
	}
	switch b := block.(type) {
	case webvtt.Cue:
		fmt.Printf("Cue: %s\n", b.Voices[0].Text)
	case webvtt.Note:
		fmt.Printf("Note: %s\n", b.Text)
	}
}
```

## Features

- Stream-first design with `iter.Seq2` iterator
- Parses cues, notes, styles, and regions
- Supports both timestamp formats: `HH:MM:SS.mmm` and `MM:SS.mmm`
- Handles voice/speaker tags `<v Speaker>text</v>`
- Parses cue settings (position, align, line, size, vertical, region)

## Installation

```console
% go get github.com/Songmu/go-webvtt
```

## API

```go
// Stream API - returns an iterator of blocks (Cue, Note, Style, Region)
func Parse(r io.Reader) iter.Seq2[Block, error]

// Batch API - parses all cues into a WebVTT struct
func ParseAll(r io.Reader) (*WebVTT, error)

// Lossless API - retains original bytes, every block and editable text ranges
func ParseDocument(raw []byte) (*Document, error)

// Text-only patches use absolute, half-open byte offsets in the original input
func (d *Document) Apply(patches []Patch) ([]byte, error)

// Visible cue text after patches, including read-only character references
func (d *Document) CueText(patches []Patch) ([]string, error)
```

### Lossless editing

```go
doc, err := webvtt.ParseDocument(raw)
if err != nil {
	return err
}
node := doc.TextNodes[0]
out, err := doc.Apply([]webvtt.Patch{
	{Start: node.Start, End: node.End, Replacement: "Corrected text"},
})
```

`Document.Raw` retains the input, and `Document.Blocks` includes cues, NOTE,
STYLE and REGION blocks with their byte ranges. `TextNodes` are nonempty text
runs with original byte offsets, zero-based cue indices and enclosing voice
annotations. Tags (including inline timestamps), character references and line
endings are read-only and excluded from these ranges. Character-reference-shaped
sequences are conservatively protected even when their names are unknown.

UTF-8 BOM, CR, CRLF, LF, mixed line endings and long lines are supported by both
parsing APIs. `ParseDocument` rejects malformed UTF-8, NUL, malformed timestamps,
out-of-range minutes/seconds, duration overflow and non-increasing cue intervals.
The legacy parser retains its existing extended-minute `MM:SS.mmm` support.
Header metadata is preserved before the first blank line.

`Apply(nil)` returns bytes identical to the input. Edits never mutate the
document, and later calls still use original offsets. Public slices are snapshots:
changing them does not change the writer's source or permitted ranges. Patches
must be disjoint, fit within one text node and use UTF-8 boundaries. Replacements
must be valid UTF-8 and cannot contain `<`, `>`, `&`, NUL or line endings.
Patches that create a new block separator or otherwise alter block/cue structure
are rejected. Empty cue payloads are allowed; applications can use `CueText` to
enforce their own visibility policy. This API is a structural parser and safe
text writer, not a complete WebVTT rendering or CSS conformance validator.

## Author

[Songmu](https://github.com/Songmu)
