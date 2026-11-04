// Package output prints command results.
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Humaner is a value that has a text form for --human.
type Humaner interface{ Human() string }

// Print writes v as JSON with a 2-space indent and a final newline.
// With human, it writes v.Human() when v has one.
func Print(w io.Writer, v any, human bool) error {
	if h, ok := v.(Humaner); ok && human {
		_, err := fmt.Fprintln(w, h.Human())
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
