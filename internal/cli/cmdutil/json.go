package cmdutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// WriteJSON writes v as indented JSON followed by a newline. Output is stable
// across runs: 2-space indentation, struct field order, no HTML escaping.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// WriteJSONField writes v as WriteJSON does, with one more field after its
// own when v is a JSON object. Any other value, an array for one, has no
// place for a field and is written as it is.
func WriteJSONField(w io.Writer, v any, key string, field any) error {
	var obj, extra bytes.Buffer
	if err := WriteJSON(&obj, v); err != nil {
		return err
	}
	body, isObject := bytes.CutSuffix(obj.Bytes(), []byte("}\n"))
	if !isObject {
		_, err := w.Write(obj.Bytes())
		return err
	}
	enc := json.NewEncoder(&extra)
	enc.SetEscapeHTML(false)
	enc.SetIndent("  ", "  ")
	if err := enc.Encode(field); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	sep := ",\n"
	if body = bytes.TrimSuffix(body, []byte("\n")); len(body) == 1 {
		sep = "\n"
	}
	_, err := fmt.Fprintf(w, "%s%s  %q: %s}\n", body, sep, key, extra.Bytes())
	return err
}
