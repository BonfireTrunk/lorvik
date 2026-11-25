package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// parseTOML reads the flat subset of TOML the config files use: `key = "value"`
// lines, `#` comments and blank lines. Reading stops at the first table
// header, so a table's entries never read as root keys. A bare true or false
// is read as that word, so a switch written without quotes still counts;
// other non-string values are skipped so a richer file written by hand does
// not break the CLI.
func parseTOML(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			break
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = \"value\"", n)
		}
		val = strings.TrimSpace(val)
		switch {
		case strings.HasPrefix(val, `"`):
			end := closingQuote(val)
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated string", n)
			}
			s, err := strconv.Unquote(val[:end+1])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			out[strings.TrimSpace(key)] = s
		case strings.HasPrefix(val, `'`):
			end := strings.Index(val[1:], "'")
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated string", n)
			}
			out[strings.TrimSpace(key)] = val[1 : end+1]
		default:
			if word, _, _ := strings.Cut(val, "#"); strings.TrimSpace(word) == "true" || strings.TrimSpace(word) == "false" {
				out[strings.TrimSpace(key)] = strings.TrimSpace(word)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read toml: %w", err)
	}
	return out, nil
}

// closingQuote returns the index of the unescaped quote that ends val, which
// starts with a quote, or -1.
func closingQuote(val string) int {
	for i := 1; i < len(val); i++ {
		switch val[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// renderTOML writes flat string pairs in the order given.
func renderTOML(pairs ...[2]string) string {
	var b strings.Builder
	for _, p := range pairs {
		if p[1] != "" {
			fmt.Fprintf(&b, "%s = %s\n", p[0], strconv.Quote(p[1]))
		}
	}
	return b.String()
}
