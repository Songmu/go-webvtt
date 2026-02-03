package webvtt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"iter"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BlockType represents the type of a WebVTT block
type BlockType int

const (
	BlockTypeCue BlockType = iota
	BlockTypeNote
	BlockTypeStyle
	BlockTypeRegion
)

// Block is an interface for WebVTT blocks
type Block interface {
	blockType() BlockType
}

// WebVTT represents a parsed WebVTT file
type WebVTT struct {
	Cues []Cue
}

// Cue represents a WebVTT cue block
type Cue struct {
	ID        string
	StartTime time.Duration
	EndTime   time.Duration
	Settings  CueSettings
	Voices    []Voice
}

func (c Cue) blockType() BlockType { return BlockTypeCue }

// CueSettings represents cue settings
type CueSettings struct {
	Vertical string
	Line     string
	Position string
	Size     string
	Align    string
	Region   string
}

// Voice represents a voice span in cue text
type Voice struct {
	Speaker string
	Text    string
}

// Note represents a NOTE block
type Note struct {
	Text string
}

func (n Note) blockType() BlockType { return BlockTypeNote }

// Style represents a STYLE block
type Style struct {
	Text string
}

func (s Style) blockType() BlockType { return BlockTypeStyle }

// Region represents a REGION block
type Region struct {
	ID       string
	Settings map[string]string
}

func (r Region) blockType() BlockType { return BlockTypeRegion }

var (
	// Matches timestamp line: "00:00:01.000 --> 00:00:04.000" with optional settings
	timestampLineRegex = regexp.MustCompile(`^(\d{1,2}:)?\d{2}:\d{2}\.\d{3}\s+-->\s+(\d{1,2}:)?\d{2}:\d{2}\.\d{3}`)
	// Matches voice tag: <v Name>
	voiceStartRegex = regexp.MustCompile(`<v\s+([^>]+)>`)
)

// Parse parses WebVTT content and returns an iterator of blocks
func Parse(r io.Reader) iter.Seq2[Block, error] {
	return func(yield func(Block, error) bool) {
		scanner := bufio.NewScanner(r)

		// Check WEBVTT header
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				yield(nil, err)
			} else {
				yield(nil, errors.New("empty input"))
			}
			return
		}
		header := scanner.Text()
		if !strings.HasPrefix(header, "WEBVTT") {
			yield(nil, errors.New("missing WEBVTT header"))
			return
		}

		var lines []string
		for scanner.Scan() {
			line := scanner.Text()

			if line == "" {
				// Empty line = end of block
				if len(lines) > 0 {
					block, err := parseBlock(lines)
					if err != nil {
						if !yield(nil, err) {
							return
						}
					} else if block != nil {
						if !yield(block, nil) {
							return
						}
					}
					lines = nil
				}
				continue
			}
			lines = append(lines, line)
		}

		// Handle last block
		if len(lines) > 0 {
			block, err := parseBlock(lines)
			if err != nil {
				yield(nil, err)
				return
			}
			if block != nil {
				if !yield(block, nil) {
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
		}
	}
}

// ParseAll parses WebVTT content and returns a WebVTT struct containing all cues
func ParseAll(r io.Reader) (*WebVTT, error) {
	vtt := &WebVTT{}
	for block, err := range Parse(r) {
		if err != nil {
			return nil, err
		}
		if cue, ok := block.(Cue); ok {
			vtt.Cues = append(vtt.Cues, cue)
		}
	}
	return vtt, nil
}

func parseBlock(lines []string) (Block, error) {
	if len(lines) == 0 {
		return nil, nil
	}

	first := lines[0]

	// NOTE block
	if text, ok := strings.CutPrefix(first, "NOTE"); ok {
		text = strings.TrimSpace(text)
		if len(lines) > 1 {
			text = text + "\n" + strings.Join(lines[1:], "\n")
		}
		return Note{Text: strings.TrimSpace(text)}, nil
	}

	// STYLE block
	if first == "STYLE" {
		return Style{Text: strings.Join(lines[1:], "\n")}, nil
	}

	// REGION block
	if first == "REGION" || strings.HasPrefix(first, "REGION") {
		region := Region{Settings: make(map[string]string)}
		startIdx := 0
		if first == "REGION" {
			startIdx = 1
		} else {
			// REGION with inline settings on first line not standard, but handle it
			startIdx = 0
		}
		for _, line := range lines[startIdx:] {
			if line == "REGION" {
				continue
			}
			if idx := strings.Index(line, ":"); idx > 0 {
				key := strings.TrimSpace(line[:idx])
				value := strings.TrimSpace(line[idx+1:])
				if key == "id" {
					region.ID = value
				} else {
					region.Settings[key] = value
				}
			}
		}
		return region, nil
	}

	// Cue block
	return parseCue(lines)
}

func parseCue(lines []string) (Cue, error) {
	var cue Cue
	idx := 0

	// Check if first line is cue ID (not a timestamp line)
	if !timestampLineRegex.MatchString(lines[0]) {
		cue.ID = lines[0]
		idx = 1
	}

	if idx >= len(lines) {
		return cue, errors.New("missing timestamp line")
	}

	// Parse timestamp line
	tsLine := lines[idx]
	startTime, endTime, settings, err := parseTimestampLine(tsLine)
	if err != nil {
		return cue, err
	}
	cue.StartTime = startTime
	cue.EndTime = endTime
	cue.Settings = settings
	idx++

	// Parse text content
	if idx < len(lines) {
		text := strings.Join(lines[idx:], "\n")
		cue.Voices = parseVoices(text)
	}

	return cue, nil
}

func parseTimestampLine(line string) (start, end time.Duration, settings CueSettings, err error) {
	// Split by "-->"
	parts := strings.SplitN(line, "-->", 2)
	if len(parts) != 2 {
		return 0, 0, settings, errors.New("invalid timestamp line")
	}

	startStr := strings.TrimSpace(parts[0])
	rest := strings.TrimSpace(parts[1])

	// End time and optional settings
	restParts := strings.Fields(rest)
	if len(restParts) == 0 {
		return 0, 0, settings, errors.New("missing end timestamp")
	}

	endStr := restParts[0]

	start, err = parseTimestamp(startStr)
	if err != nil {
		return 0, 0, settings, fmt.Errorf("invalid start timestamp: %w", err)
	}

	end, err = parseTimestamp(endStr)
	if err != nil {
		return 0, 0, settings, fmt.Errorf("invalid end timestamp: %w", err)
	}

	// Parse settings
	for _, s := range restParts[1:] {
		if idx := strings.Index(s, ":"); idx > 0 {
			key := s[:idx]
			value := s[idx+1:]
			switch key {
			case "vertical":
				settings.Vertical = value
			case "line":
				settings.Line = value
			case "position":
				settings.Position = value
			case "size":
				settings.Size = value
			case "align":
				settings.Align = value
			case "region":
				settings.Region = value
			}
		}
	}

	return start, end, settings, nil
}

func parseTimestamp(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	var hours, minutes, seconds int
	var millis int
	var err error

	if len(parts) == 3 {
		// HH:MM:SS.mmm
		hours, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, err
		}
		minutes, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, err
		}
		secParts := strings.Split(parts[2], ".")
		seconds, err = strconv.Atoi(secParts[0])
		if err != nil {
			return 0, err
		}
		if len(secParts) > 1 {
			millis, err = strconv.Atoi(secParts[1])
			if err != nil {
				return 0, err
			}
		}
	} else if len(parts) == 2 {
		// MM:SS.mmm
		minutes, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, err
		}
		secParts := strings.Split(parts[1], ".")
		seconds, err = strconv.Atoi(secParts[0])
		if err != nil {
			return 0, err
		}
		if len(secParts) > 1 {
			millis, err = strconv.Atoi(secParts[1])
			if err != nil {
				return 0, err
			}
		}
	} else {
		return 0, errors.New("invalid timestamp format")
	}

	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second +
		time.Duration(millis)*time.Millisecond, nil
}

func parseVoices(text string) []Voice {
	var voices []Voice

	// Find all voice tags
	matches := voiceStartRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		// No voice tags, treat entire text as anonymous voice
		return []Voice{{Text: text}}
	}

	lastEnd := 0
	for i, match := range matches {
		// Text before this voice tag (if any, and not part of a voice)
		if match[0] > lastEnd {
			prefix := strings.TrimSpace(text[lastEnd:match[0]])
			if prefix != "" && len(voices) > 0 {
				// Append to previous voice
				voices[len(voices)-1].Text += " " + prefix
			} else if prefix != "" {
				voices = append(voices, Voice{Text: prefix})
			}
		}

		name := text[match[2]:match[3]]
		tagEnd := match[1]

		// Find the end of this voice's text
		var voiceText string
		if i+1 < len(matches) {
			// Text until next voice tag or </v>
			nextStart := matches[i+1][0]
			voiceText = text[tagEnd:nextStart]
		} else {
			// Rest of text
			voiceText = text[tagEnd:]
		}

		// Remove </v> if present
		if idx := strings.Index(voiceText, "</v>"); idx >= 0 {
			voiceText = voiceText[:idx]
			lastEnd = tagEnd + idx + 4
		} else {
			lastEnd = tagEnd + len(voiceText)
		}

		voices = append(voices, Voice{
			Speaker: name,
			Text:    strings.TrimSpace(voiceText),
		})
	}

	return voices
}
