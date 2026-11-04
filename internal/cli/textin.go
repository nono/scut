package cli

import (
	"io"
	"os"
	"strings"

	"github.com/nono/scut/internal/exit"
)

// readText reads the text of a comment or a description from a file. The path
// "-" means stdin. A text with only white space is a usage error.
func readText(path string, stdin io.Reader) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		if stdin == nil {
			return "", exit.Usagef("no input on stdin")
		}
		b, err = io.ReadAll(stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", exit.Usagef("cannot read %s: %v", path, err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", exit.Usagef("the text is empty")
	}
	return string(b), nil
}
