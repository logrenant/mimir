package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// ErrNotCSV means the bytes handed in do not parse as delimited text at all.
var ErrNotCSV = errors.New("catalog: not a readable CSV")

// Encoding names the byte encoding a file was written in. It is recorded rather
// than normalised away because the operator imports the result back into the
// same admin panel that produced it, and a panel that emitted Windows-1254 is
// not promised to accept UTF-8.
type Encoding string

const (
	EncodingUTF8    Encoding = "utf-8"
	EncodingCP1254  Encoding = "windows-1254"
	EncodingUnknown Encoding = ""
)

// Quoting is the fallback rule for a cell the source file did not have — a
// row this package had to widen, or a column an exporter left short.
//
// It is only a fallback. Which cells were quoted is recorded per cell, in
// File.Quoted, because no single rule describes a real export: Shopify quotes
// its HTML column whether or not the content needs it and quotes the rest only
// when it must, so a file-wide "minimal" and a file-wide "all" are both wrong
// about the same file.
type Quoting string

const (
	// QuoteMinimal quotes only a field that would otherwise be ambiguous —
	// the RFC 4180 rule, and Go's.
	QuoteMinimal Quoting = "minimal"
	// QuoteAll quotes every field, which is what several Turkish panels emit.
	QuoteAll Quoting = "all"
)

// File is one CSV as it was read, with everything needed to write it back
// unchanged: the header, every cell of every row, and the four framing
// decisions the original made.
//
// Rows holds the *raw* cells, all of them, including the columns this package
// has no name for. That is the lossless contract: export rewrites the mapped
// cells and copies the rest, so a column Mimir never understood survives the
// round trip untouched.
type File struct {
	Header    []string
	Rows      [][]string
	Delimiter rune
	Encoding  Encoding
	HasBOM    bool
	CRLF      bool
	Quoting   Quoting
	// Quoted mirrors Header and Rows: Quoted[0] is the header, Quoted[i+1] is
	// Rows[i]. A cell is written back with the quoting it arrived with unless
	// its new content requires quotes anyway.
	Quoted [][]bool
	// TrailingNewline is whether the file ended with a line break. Excel
	// writes one; some API exports do not, and a diff notices.
	TrailingNewline bool

	// Dialect is the platform profile that matched, or the zero value when the
	// header matched none. A zero Dialect is not an error: the operator maps
	// the columns themselves.
	Dialect Dialect
	// Mapping is the operator's own column map, used only when Dialect is zero.
	Mapping map[Field]string
	// Translations is the operator's own column map per target language, used
	// on the same terms as Mapping. A profile that already names the language's
	// columns needs nothing here.
	Translations map[Lang]map[Field]string `json:"translations,omitempty"`
	// TargetLang is the operator's answer to "which language are this file's
	// translation columns in" — the question IKAS's translations export cannot
	// answer about itself. Empty means nobody has said yet.
	TargetLang Lang `json:"target_lang,omitempty"`
	// Write is the fields a rewrite may change, as the operator configured
	// them. Empty means the default: every writable field this file actually
	// has a column for.
	//
	// It lives on the File beside Mapping because it is the same kind of thing
	// — the operator's answer about this particular export — and because a
	// product catalogue does not have a fixed field set: one store's export has
	// an SEO description and a custom Arabic body, another's has neither.
	Write []LangField `json:"write,omitempty"`
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// candidateDelimiters is the set sniffed for, most likely first. The semicolon
// is not exotic here: Excel writes it wherever the system list separator is a
// semicolon, which is every Turkish-locale machine this tool is aimed at.
var candidateDelimiters = []rune{',', ';', '\t', '|'}

// ParseCSV reads a catalog export.
//
// It answers four framing questions before it reads a single record — BOM,
// encoding, delimiter, line ending — because every one of them has to be
// replayed on the way out, and a reader that normalises them has already lost
// the information the writer needs.
func ParseCSV(r io.Reader) (File, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return File{}, fmt.Errorf("catalog: read: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return File{}, fmt.Errorf("%w: the file is empty", ErrNotCSV)
	}

	f := File{}
	if bytes.HasPrefix(raw, utf8BOM) {
		f.HasBOM = true
		raw = raw[len(utf8BOM):]
	}

	// Encoding is decided by whether the bytes are valid UTF-8. That is a test,
	// not a guess: Windows-1254 text with a Turkish letter in it is not valid
	// UTF-8, and Windows-1254 text without one is byte-identical to ASCII, so
	// the two answers never disagree about a file where it matters.
	if utf8.Valid(raw) {
		f.Encoding = EncodingUTF8
	} else {
		decoded, derr := charmap.Windows1254.NewDecoder().Bytes(raw)
		if derr != nil {
			return File{}, fmt.Errorf("%w: bytes are neither UTF-8 nor Windows-1254", ErrNotCSV)
		}
		f.Encoding = EncodingCP1254
		raw = decoded
	}

	f.CRLF = bytes.Contains(raw, []byte("\r\n"))
	f.TrailingNewline = bytes.HasSuffix(raw, []byte("\n"))
	f.Delimiter = sniffDelimiter(raw)
	f.Quoting = sniffQuoting(raw, f.Delimiter)

	records, quoted, err := readRecords(string(raw), f.Delimiter)
	if err != nil {
		return File{}, err
	}
	if len(records) == 0 {
		return File{}, fmt.Errorf("%w: no records", ErrNotCSV)
	}

	f.Header = records[0]
	f.Rows = records[1:]
	f.Quoted = quoted
	if d, ok := Detect(f.Header); ok {
		f.Dialect = d
	}
	return f, nil
}

// sniffDelimiter counts candidates outside quotes on the header line. The
// header is the right line to look at: it is the one line guaranteed to have
// every column and no embedded newline.
func sniffDelimiter(raw []byte) rune {
	line := raw
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		line = raw[:i]
	}
	best, bestN := ',', -1
	for _, d := range candidateDelimiters {
		if n := countOutsideQuotes(string(line), d); n > bestN {
			best, bestN = d, n
		}
	}
	if bestN <= 0 {
		return ','
	}
	return best
}

func countOutsideQuotes(s string, d rune) int {
	n, inQuote := 0, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == d && !inQuote:
			n++
		}
	}
	return n
}

// sniffQuoting picks the fallback rule for cells that were never in the source.
// It reads the header because that is the one row always fully populated.
func sniffQuoting(raw []byte, d rune) Quoting {
	line := raw
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		line = raw[:i]
	}
	s := strings.TrimRight(string(line), "\r")
	if s == "" {
		return QuoteMinimal
	}
	fields, inQuote, cur := 0, false, strings.Builder{}
	quotedAll := true
	flush := func() {
		v := cur.String()
		cur.Reset()
		if v == "" {
			return
		}
		fields++
		if len(v) < 2 || !strings.HasPrefix(v, `"`) || !strings.HasSuffix(v, `"`) {
			quotedAll = false
		}
	}
	for _, r := range s {
		if r == '"' {
			inQuote = !inQuote
		}
		if r == d && !inQuote {
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	if fields == 0 || !quotedAll {
		return QuoteMinimal
	}
	return QuoteAll
}

// WriteCSV replays a File's framing decisions exactly: its BOM, its encoding,
// its delimiter, its line ending, its trailing newline, and the quoting of
// every cell it originally had.
//
// It does not use encoding/csv's Writer. That writer owns three of those
// decisions — it always writes CRLF or always LF by a boolean, it always
// quotes minimally, and it always ends with a newline — so replaying a real
// export through it silently rewrites every line of a file the operator is
// about to hand back to their own admin panel.
func WriteCSV(f File, rows [][]string) ([]byte, error) {
	nl := "\n"
	if f.CRLF {
		nl = "\r\n"
	}
	d := f.Delimiter
	if d == 0 {
		d = ','
	}

	var b strings.Builder
	writeRow := func(cells []string, was []bool) {
		for i, c := range cells {
			if i > 0 {
				b.WriteRune(d)
			}
			b.WriteString(quoteField(c, d, quotedBefore(was, i, f.Quoting)))
		}
		b.WriteString(nl)
	}
	writeRow(f.Header, quotedRow(f, 0))
	for i, row := range rows {
		writeRow(row, quotedRow(f, i+1))
	}

	out := b.String()
	if !f.TrailingNewline {
		out = strings.TrimSuffix(out, nl)
	}

	raw := []byte(out)
	if f.Encoding == EncodingCP1254 {
		enc, err := charmap.Windows1254.NewEncoder().Bytes(raw)
		if err != nil {
			// A rewrite introduced a character the source encoding cannot
			// carry. Saying so is the only honest answer: dropping it silently
			// would hand back a file whose text is not what was approved.
			return nil, fmt.Errorf("catalog: content does not fit %s: %w", f.Encoding, err)
		}
		raw = enc
	}
	if f.HasBOM {
		raw = append(append([]byte{}, utf8BOM...), raw...)
	}
	return raw, nil
}

func quotedRow(f File, i int) []bool {
	if i < 0 || i >= len(f.Quoted) {
		return nil
	}
	return f.Quoted[i]
}

// quotedBefore is whether this cell arrived quoted. A cell the source never had
// falls back to the file's own rule.
func quotedBefore(was []bool, i int, fallback Quoting) bool {
	if i < len(was) {
		return was[i]
	}
	return fallback == QuoteAll
}

// quoteField quotes when the source did, and always when the content leaves no
// choice — a rewrite that introduced a comma must be quoted whatever the
// original cell looked like.
func quoteField(s string, d rune, wasQuoted bool) string {
	needs := wasQuoted ||
		strings.ContainsRune(s, d) ||
		strings.ContainsAny(s, "\"\r\n")
	if !needs {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// readRecords is an RFC 4180 reader that also reports which cells were quoted.
//
// encoding/csv cannot answer that question, and it is the question this package
// has to answer: an exporter that wrapped its HTML column in quotes it did not
// need will reject its own file back if the quotes are gone. So the reader is
// ours — sixty lines against a re-quoted file the operator cannot import.
func readRecords(s string, d rune) ([][]string, [][]bool, error) {
	var (
		records [][]string
		quoting [][]bool
		row     []string
		rowQ    []bool
		cur     strings.Builder
		inQuote bool
		wasQ    bool
	)
	endField := func() {
		row = append(row, cur.String())
		rowQ = append(rowQ, wasQ)
		cur.Reset()
		wasQ = false
	}
	endRecord := func() {
		endField()
		records = append(records, row)
		quoting = append(quoting, rowQ)
		row, rowQ = nil, nil
	}

	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if inQuote {
			if c != '"' {
				cur.WriteRune(c)
				continue
			}
			// A doubled quote inside a quoted field is one literal quote.
			if i+1 < len(rs) && rs[i+1] == '"' {
				cur.WriteRune('"')
				i++
				continue
			}
			inQuote = false
			continue
		}
		switch {
		case c == '"' && cur.Len() == 0:
			inQuote, wasQ = true, true
		case c == d:
			endField()
		case c == '\r':
			// Swallowed: the line ending was decided once, on the whole file.
		case c == '\n':
			endRecord()
		default:
			cur.WriteRune(c)
		}
	}
	if inQuote {
		return nil, nil, fmt.Errorf("%w: an unterminated quoted field", ErrNotCSV)
	}
	// A final record with no trailing newline is still a record; an empty tail
	// after one is not.
	if cur.Len() > 0 || len(row) > 0 || wasQ {
		endRecord()
	}
	return records, quoting, nil
}
