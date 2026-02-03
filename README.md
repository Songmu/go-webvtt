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
```

## Author

[Songmu](https://github.com/Songmu)
