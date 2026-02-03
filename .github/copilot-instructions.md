# Copilot Instructions for go-webvtt

A Go library for parsing WebVTT (Web Video Text Tracks) files.

## Build & Test Commands

```bash
# Run all tests
go test ./...

# Run all tests with race detection and coverage
go test -race -coverprofile coverage.out -covermode atomic ./...

# Run a specific test
go test -run TestName ./...

# Update golden files
UPDATE_GOLDEN=1 go test ./...

# Update dependencies
make deps
```

## WebVTT Format

WebVTT files contain timed text cues for video subtitles/captions. Key format elements:

- Files must start with `WEBVTT` header
- Cues have optional identifiers, timestamps, and text content
- Timestamp format: `HH:MM:SS.mmm --> HH:MM:SS.mmm` or `MM:SS.mmm --> MM:SS.mmm`
- Supports voice/speaker tags: `<v Speaker Name>text</v>` (end tag optional)
- Multiple voices allowed per cue
- Inline tags (`<i>`, `<b>`, `<u>`, etc.) are preserved as raw text
- STYLE, REGION, and NOTE blocks are parsed but not deeply interpreted

Example cue structure:
```
cue-identifier
00:00:03.142 --> 00:00:03.901
<v Speaker A>Hello world.</v>
```

## Conventions

- Package name is `webvtt` (not `go-webvtt`)
- Version management uses [godzil](https://github.com/Songmu/godzil) - do not manually edit version.go for releases
- Test data files go in `testdata/` directory
- Invalid test files use `invalid-*.vtt` naming convention
