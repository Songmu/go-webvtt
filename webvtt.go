package webvtt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	// Matches voice tag: <v Name>
	voiceStartRegex     = regexp.MustCompile(`<v\s+([^>]+)>`)
	timestampArrowRegex = regexp.MustCompile(`[ \t]+-->[ \t]+`)
)

// Parse parses WebVTT content and returns an iterator of blocks
func Parse(r io.Reader) iter.Seq2[Block, error] {
	return func(yield func(Block, error) bool) {
		scanner := bufio.NewScanner(r)
		scanner.Split(splitWebVTTLines)
		scanner.Buffer(make([]byte, 4096), math.MaxInt)

		if err := scanHeader(scanner); err != nil {
			yield(nil, err)
			return
		}

		var lines []string
		for scanner.Scan() {
			line := scanner.Text()
			if !utf8.ValidString(line) || strings.ContainsRune(line, '\x00') {
				yield(nil, errors.New("WebVTT must be valid UTF-8 without NUL"))
				return
			}
			if line == "" {
				if more, _ := yieldParsedBlock(lines, yield); !more {
					return
				}
				lines = nil
				continue
			}
			lines = append(lines, line)
		}

		// A malformed final block takes precedence over a scanner error.
		if more, err := yieldParsedBlock(lines, yield); !more || err != nil {
			return
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
		}
	}
}

func scanHeader(scanner *bufio.Scanner) error {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return errors.New("empty input")
	}
	header := scanner.Text()
	if !utf8.ValidString(header) || strings.ContainsRune(header, '\x00') || !validHeader(header) {
		return errors.New("missing WEBVTT header")
	}
	for scanner.Scan() {
		line := scanner.Text()
		if !utf8.ValidString(line) || strings.ContainsRune(line, '\x00') {
			return errors.New("WebVTT must be valid UTF-8 without NUL")
		}
		if line == "" {
			return nil
		}
		if strings.Contains(line, "-->") {
			return errors.New("missing blank line after WEBVTT header")
		}
	}
	return scanner.Err()
}

// yieldParsedBlock reports parse errors as well as blocks. The returned error
// has already been yielded; the caller decides whether parsing can continue.
func yieldParsedBlock(lines []string, yield func(Block, error) bool) (bool, error) {
	block, err := parseBlock(lines)
	if err != nil {
		return yield(nil, err), err
	}
	if block != nil {
		return yield(block, nil), nil
	}
	return true, nil
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
	return parseBlockWithTimestampPolicy(lines, true)
}

func parseBlockWithTimestampPolicy(lines []string, allowExtendedMinutes bool) (Block, error) {
	if len(lines) == 0 {
		return nil, nil
	}

	first := lines[0]

	// NOTE block
	if text, ok := strings.CutPrefix(first, "NOTE"); ok && (text == "" || strings.HasPrefix(text, " ") || strings.HasPrefix(text, "\t")) {
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
	if first == "REGION" || strings.HasPrefix(first, "REGION ") || strings.HasPrefix(first, "REGION\t") {
		return parseRegion(lines), nil
	}

	// Cue block
	return parseCue(lines, allowExtendedMinutes)
}

func parseRegion(lines []string) Region {
	region := Region{Settings: make(map[string]string)}
	// Preserve nonstandard inline settings, including the REGION prefix in keys.
	for _, line := range lines {
		if line == "REGION" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if key == "id" {
			region.ID = value
		} else {
			region.Settings[key] = value
		}
	}
	return region
}

func parseCue(lines []string, allowExtendedMinutes bool) (Cue, error) {
	var cue Cue
	idx := 0

	// Check if first line is cue ID (not a timestamp line)
	if !strings.Contains(lines[0], "-->") {
		cue.ID = lines[0]
		idx = 1
	}

	if idx >= len(lines) {
		return cue, errors.New("missing timestamp line")
	}

	// Parse timestamp line
	tsLine := lines[idx]
	startTime, endTime, settings, err := parseTimestampLine(tsLine, allowExtendedMinutes)
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

func parseTimestampLine(line string, allowExtendedMinutes bool) (start, end time.Duration, settings CueSettings, err error) {
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
	if end <= start {
		return 0, 0, settings, errors.New("end timestamp must be greater than start timestamp")
	}
	if !timestampArrowRegex.MatchString(line) {
		return 0, 0, settings, errors.New("timestamp arrow must be surrounded by whitespace")
	}
	// Document range checks follow all common errors and remain unwrapped.
	if !allowExtendedMinutes && (hasExtendedMinutes(startStr, start) || hasExtendedMinutes(endStr, end)) {
		return 0, 0, settings, errors.New("timestamp minutes and seconds must be below 60")
	}

	settings = parseCueSettings(restParts[1:])
	return start, end, settings, nil
}

func parseCueSettings(parts []string) CueSettings {
	var settings CueSettings
	for _, s := range parts {
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

	return settings
}

func hasExtendedMinutes(timestamp string, value time.Duration) bool {
	return strings.Count(timestamp, ":") == 1 && value >= time.Hour
}

func parseTimestamp(s string) (time.Duration, error) {
	return parseTimestampValue(s, true)
}

func parseTimestampValue(s string, allowExtendedMinutes bool) (time.Duration, error) {
	if !strictTimestampRegex.MatchString(s) {
		return 0, errors.New("invalid timestamp format")
	}
	parts := strings.Split(s, ":")
	short := len(parts) == 2
	var hours uint64
	if len(parts) == 3 {
		var err error
		hours, err = strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return 0, errors.New("timestamp overflow")
		}
		parts = parts[1:]
	}
	minutes, _ := strconv.ParseUint(parts[0], 10, 64)
	seconds, _ := strconv.ParseUint(parts[1][:2], 10, 64)
	millis, _ := strconv.ParseUint(parts[1][3:], 10, 64)
	if (minutes > 59 && !(short && allowExtendedMinutes)) || seconds > 59 {
		return 0, errors.New("timestamp minutes and seconds must be below 60")
	}
	maxMillis := uint64(math.MaxInt64 / int64(time.Millisecond))
	remainder := minutes*60000 + seconds*1000 + millis
	if hours > maxMillis/3600000 || hours*3600000 > maxMillis-remainder {
		return 0, errors.New("timestamp overflow")
	}
	return time.Duration(hours*3600000+remainder) * time.Millisecond, nil
}

var strictTimestampRegex = regexp.MustCompile(`^([0-9]{2,}:)?[0-9]{2}:[0-9]{2}\.[0-9]{3}$`)

func splitWebVTTLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b != '\r' && b != '\n' {
			continue
		}
		if b == '\r' && i+1 == len(data) && !atEOF {
			return 0, nil, nil
		}
		next := i + 1
		if b == '\r' && next < len(data) && data[next] == '\n' {
			next++
		}
		return next, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
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
