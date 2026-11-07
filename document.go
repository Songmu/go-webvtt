package webvtt

import (
	"bytes"
	"fmt"
	"html"
	"maps"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Document retains the original bytes and every parsed block. All ranges are
// half-open, absolute byte offsets into Raw. Apply uses an immutable snapshot.
type Document struct {
	Raw       []byte
	Blocks    []LocatedBlock
	TextNodes []TextNode
	raw       []byte
	nodes     []TextNode
	entities  []entityNode
	cueCount  int
	blocks    []LocatedBlock
}

// LocatedBlock pairs a parsed block with its original range, excluding the
// blank separator lines. Cue is zero-based, or -1 for non-cue blocks.
type LocatedBlock struct {
	Block      Block
	Start, End int
	Cue        int
}

// TextNode is an editable run of cue text, excluding tags, character references
// and line endings. Speaker is the enclosing voice annotation, if any.
type TextNode struct {
	Start, End int
	Cue        int
	Speaker    string
}

// Patch replaces a range of the original input, not a previously patched input.
type Patch struct {
	Start, End  int
	Replacement string
}

type sourceLine struct {
	start, end, next int
}

type entityNode struct {
	Start, End, Cue int
}

func cloneLocatedBlocks(blocks []LocatedBlock) []LocatedBlock {
	blocks = append([]LocatedBlock(nil), blocks...)
	for i, loc := range blocks {
		if region, ok := loc.Block.(Region); ok {
			region.Settings = maps.Clone(region.Settings)
			blocks[i].Block = region
		}
	}
	return blocks
}

func sourceLines(raw []byte) []sourceLine {
	var lines []sourceLine
	for start := 0; start < len(raw); {
		end := start
		for end < len(raw) && raw[end] != '\r' && raw[end] != '\n' {
			end++
		}
		next := end
		if next < len(raw) {
			next++
			if raw[end] == '\r' && next < len(raw) && raw[next] == '\n' {
				next++
			}
		}
		lines = append(lines, sourceLine{start, end, next})
		start = next
	}
	return lines
}

func validHeader(header string) bool {
	header = strings.TrimPrefix(header, "\ufeff")
	return (header == "WEBVTT" || strings.HasPrefix(header, "WEBVTT ") ||
		strings.HasPrefix(header, "WEBVTT\t")) && !strings.Contains(header, "-->")
}

// ParseDocument parses a UTF-8 WebVTT file without normalizing any bytes.
// BOM, CR, CRLF, LF, mixed line endings and arbitrarily long lines are supported.
// Malformed blocks, timestamps and UTF-8 are reported rather than skipped.
func ParseDocument(raw []byte) (*Document, error) {
	if !utf8.Valid(raw) || bytes.ContainsRune(raw, '\x00') {
		return nil, fmt.Errorf("WebVTT must be valid UTF-8 without NUL")
	}
	lines := sourceLines(raw)
	if len(lines) == 0 || !validHeader(string(raw[lines[0].start:lines[0].end])) {
		return nil, fmt.Errorf("missing or invalid WEBVTT header")
	}
	start, err := documentBodyStart(raw, lines)
	if err != nil {
		return nil, err
	}
	d := &Document{raw: bytes.Clone(raw), Raw: bytes.Clone(raw)}
	if err := d.collectBlocks(raw, lines, start); err != nil {
		return nil, err
	}
	d.nodes = append([]TextNode(nil), d.TextNodes...)
	d.blocks = cloneLocatedBlocks(d.Blocks)
	return d, nil
}

func documentBodyStart(raw []byte, lines []sourceLine) (int, error) {
	i := 1
	// Header metadata belongs to the header, not to a cue.
	for i < len(lines) && lines[i].start != lines[i].end {
		if bytes.Contains(raw[lines[i].start:lines[i].end], []byte("-->")) {
			return 0, fmt.Errorf("missing blank line after WEBVTT header")
		}
		i++
	}
	return i, nil
}

func (d *Document) collectBlocks(raw []byte, lines []sourceLine, i int) error {
	for i < len(lines) {
		if lines[i].start == lines[i].end {
			i++
			continue
		}
		first := i
		text, next := documentBlockLines(raw, lines, first)
		i = next
		block, err := parseBlockWithTimestampPolicy(text, false)
		if err != nil {
			return fmt.Errorf("block at byte %d: %w", lines[first].start, err)
		}
		loc := LocatedBlock{Block: block, Start: lines[first].start, End: lines[i-1].next, Cue: -1}
		if cue, ok := block.(Cue); ok {
			loc.Cue = d.cueCount
			payload := first + 1
			if !strings.Contains(text[0], "-->") {
				payload++
			}
			if err := d.collectCuePayload(raw, lines[payload:i], cue, loc.Cue); err != nil {
				return fmt.Errorf("cue at byte %d: %w", loc.Start, err)
			}
			d.cueCount++
		}
		d.Blocks = append(d.Blocks, loc)
	}
	return nil
}

func documentBlockLines(raw []byte, lines []sourceLine, first int) ([]string, int) {
	var text []string
	i := first
	for i < len(lines) && lines[i].start != lines[i].end {
		text = append(text, string(raw[lines[i].start:lines[i].end]))
		i++
	}
	return text, i
}

func (d *Document) collectCuePayload(raw []byte, lines []sourceLine, cue Cue, cueIndex int) error {
	speaker := ""
	var voices []string
	lastTimestamp := cue.StartTime
	for _, line := range lines {
		nodes, entities, err := scanTextNodes(raw, line, cueIndex, &speaker, &voices, &lastTimestamp, cue.EndTime)
		if err != nil {
			return err
		}
		d.TextNodes = append(d.TextNodes, nodes...)
		d.entities = append(d.entities, entities...)
	}
	return nil
}

func scanTextNodes(raw []byte, line sourceLine, cue int, speaker *string, voices *[]string, lastTimestamp *time.Duration, cueEnd time.Duration) ([]TextNode, []entityNode, error) {
	var nodes []TextNode
	var entities []entityNode
	run := line.start
	flush := func(end int) {
		if end > run {
			nodes = append(nodes, TextNode{Start: run, End: end, Cue: cue, Speaker: *speaker})
		}
	}
	for i := line.start; i < line.end; {
		switch raw[i] {
		case '<':
			flush(i)
			tag, end, closed := scanTextTag(raw, i, line.end)
			if closed {
				if err := applyTextTag(tag, speaker, voices, lastTimestamp, cueEnd); err != nil {
					return nil, nil, err
				}
			}
			i, run = end, end
			continue
		case '&':
			if end := characterReferenceEnd(raw, i, line.end); end > i {
				flush(i)
				entities = append(entities, entityNode{Start: i, End: end, Cue: cue})
				i, run = end, end
				continue
			}
		}
		i++
	}
	flush(line.end)
	return nodes, entities, nil
}

func scanTextTag(raw []byte, start, lineEnd int) (tag string, end int, closed bool) {
	offset := bytes.IndexByte(raw[start+1:lineEnd], '>')
	if offset < 0 {
		// An unclosed tag consumes the rest of this line without changing state.
		return "", lineEnd, false
	}
	end = start + 1 + offset
	return string(raw[start+1 : end]), end + 1, true
}

func applyTextTag(tag string, speaker *string, voices *[]string, lastTimestamp *time.Duration, cueEnd time.Duration) error {
	if len(tag) > 0 && tag[0] >= '0' && tag[0] <= '9' {
		stamp, err := parseTimestampValue(tag, false)
		if err != nil || stamp <= *lastTimestamp || stamp >= cueEnd {
			return fmt.Errorf("invalid inline timestamp <%s>", tag)
		}
		*lastTimestamp = stamp
	}

	name := strings.Fields(tag)
	if len(name) > 1 && (name[0] == "v" || strings.HasPrefix(name[0], "v.")) {
		*voices = append(*voices, *speaker)
		// Extract from the original tag, not from whitespace-normalized fields.
		*speaker = strings.TrimSpace(tag[len(name[0]):])
		return nil
	}
	if tag != "/v" {
		return nil
	}
	*speaker = ""
	if n := len(*voices); n > 0 {
		*speaker = (*voices)[n-1]
		*voices = (*voices)[:n-1]
	}
	return nil
}

func characterReferenceEnd(raw []byte, start, lineEnd int) int {
	end := start + 1
	for end < lineEnd && ((raw[end] >= 'a' && raw[end] <= 'z') ||
		(raw[end] >= 'A' && raw[end] <= 'Z') || (raw[end] >= '0' && raw[end] <= '9') || raw[end] == '#') {
		end++
	}
	// Unknown references are still read-only; incomplete sequences remain text.
	if end > start+1 && end < lineEnd && raw[end] == ';' {
		return end + 1
	}
	return start
}

// Apply returns losslessly patched bytes. Patches must be disjoint, lie within
// a single text node and use UTF-8 boundaries. Replacements cannot introduce
// markup, character references, NUL or line endings. The document is not mutated.
// Empty payloads are permitted; applications may impose stricter policies.
func (d *Document) Apply(patches []Patch) ([]byte, error) {
	if d == nil || d.raw == nil {
		return nil, fmt.Errorf("document was not created by ParseDocument")
	}
	ordered, err := d.validatePatches(patches)
	if err != nil {
		return nil, err
	}
	out := d.applyOrderedPatches(ordered)
	if len(ordered) > 0 {
		if err := d.validatePatchedStructure(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (d *Document) validatePatches(patches []Patch) ([]Patch, error) {
	ordered := append([]Patch(nil), patches...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Start == ordered[j].Start {
			return ordered[i].End < ordered[j].End
		}
		return ordered[i].Start < ordered[j].Start
	})
	for i, p := range ordered {
		if err := d.validatePatch(p); err != nil {
			// Preserve the original non-wrapping patch errors.
			return nil, fmt.Errorf("patch %d: %s", i, err)
		}
		if i > 0 && (p.Start < ordered[i-1].End || p.Start == ordered[i-1].Start) {
			return nil, fmt.Errorf("patch %d: overlapping patches", i)
		}
	}
	return ordered, nil
}

func (d *Document) validatePatch(p Patch) error {
	if p.Start < 0 || p.End < p.Start || p.End > len(d.raw) ||
		(p.Start < len(d.raw) && !utf8.RuneStart(d.raw[p.Start])) ||
		(p.End < len(d.raw) && !utf8.RuneStart(d.raw[p.End])) {
		return fmt.Errorf("invalid UTF-8 range")
	}
	if !utf8.ValidString(p.Replacement) || strings.ContainsAny(p.Replacement, "<>&\r\n\x00") {
		return fmt.Errorf("replacement is not plain text")
	}
	for _, node := range d.nodes {
		if p.Start >= node.Start && p.End <= node.End {
			return nil
		}
	}
	return fmt.Errorf("range is not inside a text node")
}

func (d *Document) applyOrderedPatches(ordered []Patch) []byte {
	var out bytes.Buffer
	at := 0
	for _, p := range ordered {
		out.Write(d.raw[at:p.Start])
		out.WriteString(p.Replacement)
		at = p.End
	}
	out.Write(d.raw[at:])
	return out.Bytes()
}

func (d *Document) validatePatchedStructure(raw []byte) error {
	parsed, err := ParseDocument(raw)
	if err != nil {
		return fmt.Errorf("patches change WebVTT structure: %w", err)
	}
	if len(parsed.Blocks) != len(d.blocks) {
		return fmt.Errorf("patches change block count")
	}
	if len(parsed.entities) != len(d.entities) {
		return fmt.Errorf("patches introduce character references")
	}
	for i, original := range d.blocks {
		if err := validateBlockStructure(original.Block, parsed.Blocks[i].Block); err != nil {
			return err
		}
	}
	return nil
}

func validateBlockStructure(original, patched Block) error {
	if cue, ok := original.(Cue); ok {
		other, ok := patched.(Cue)
		if !ok || cue.ID != other.ID || cue.StartTime != other.StartTime ||
			cue.EndTime != other.EndTime || cue.Settings != other.Settings {
			return fmt.Errorf("patches change cue structure")
		}
		return nil
	}
	if !reflect.DeepEqual(original, patched) {
		return fmt.Errorf("patches change non-cue block")
	}
	return nil
}

// CueText returns the visible text of each cue after patches, including
// read-only character references. Tags are excluded; payload line endings are
// represented by LF. It is useful for application-level empty-cue policies.
func (d *Document) CueText(patches []Patch) ([]string, error) {
	if _, err := d.Apply(patches); err != nil {
		return nil, err
	}
	type fragment struct {
		start, end, cue int
		text            string
	}
	var fragments []fragment
	for _, node := range d.nodes {
		var edits []Patch
		for _, p := range patches {
			if p.Start >= node.Start && p.End <= node.End {
				edits = append(edits, p)
			}
		}
		sort.SliceStable(edits, func(i, j int) bool { return edits[i].Start < edits[j].Start })
		at := node.Start
		value := ""
		for _, p := range edits {
			value += string(d.raw[at:p.Start]) + p.Replacement
			at = p.End
		}
		value += string(d.raw[at:node.End])
		fragments = append(fragments, fragment{node.Start, node.End, node.Cue, value})
	}
	for _, entity := range d.entities {
		fragments = append(fragments, fragment{entity.Start, entity.End, entity.Cue,
			decodeCharacterReference(string(d.raw[entity.Start:entity.End]))})
	}
	sort.Slice(fragments, func(i, j int) bool { return fragments[i].start < fragments[j].start })
	text := make([]string, d.cueCount)
	for i, f := range fragments {
		if i > 0 && fragments[i-1].cue == f.cue &&
			bytes.ContainsAny(d.raw[fragments[i-1].end:f.start], "\r\n") {
			text[f.cue] += "\n"
		}
		text[f.cue] += f.text
	}
	return text, nil
}

func decodeCharacterReference(s string) string {
	return html.UnescapeString(s)
}
