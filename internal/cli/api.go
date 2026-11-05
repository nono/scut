package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/exit"
)

var apiMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// fieldValue returns the JSON value of a -f value: a JSON number, true,
// false or null as is, anything else as a string.
func fieldValue(s string) any {
	var v any
	if json.Valid([]byte(s)) && json.Unmarshal([]byte(s), &v) == nil {
		switch v.(type) {
		case float64, bool, nil:
			return json.RawMessage(s)
		}
	}
	return s
}

func newAPI(env Env) *cobra.Command {
	var method, input string
	var fields []string
	var yes bool
	cmd := &cobra.Command{
		Use:   "api <path>",
		Short: "Send a raw request to the API",
		Long: "Send a raw request. A path that starts with /api/ is sent as given;\n" +
			"any other path is relative to /api/v4/<workspace>.\n" +
			"The output is the entity or the array of entities of the response.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{OpsAnnotation: "*"},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check the flags before the token is read.
			var body any
			switch {
			case input != "" && len(fields) > 0:
				return exit.Usagef("--input and -f cannot be used together")
			case input != "":
				b, err := readInput(env, input)
				if err != nil {
					return err
				}
				if !json.Valid(b) {
					return exit.Usagef("the input is not valid JSON")
				}
				body = json.RawMessage(b)
			case len(fields) > 0:
				m := map[string]any{}
				for _, f := range fields {
					k, v, ok := strings.Cut(f, "=")
					if !ok || k == "" {
						return exit.Usagef("-f %q is not key=value", f)
					}
					m[k] = fieldValue(v)
				}
				body = m
			}
			m := strings.ToUpper(method)
			if m == "" {
				m = "GET"
				if body != nil {
					m = "POST"
				}
			}
			if !apiMethods[m] {
				return exit.Usagef("method %q is not valid, use GET, POST, PUT, PATCH or DELETE", method)
			}
			if m == "DELETE" && !yes {
				return exit.Usagef("DELETE needs --yes")
			}
			return withDeps(env, func(cmd *cobra.Command, args []string, d *deps) error {
				out, err := d.Doer.Raw(cmd.Context(), m, args[0], nil, body)
				if err != nil {
					return err
				}
				if len(out) == 0 {
					return nil
				}
				return d.Out(out)
			})(cmd, args)
		},
	}
	cmd.Flags().StringVarP(&method, "method", "X", "", "HTTP method (default GET, or POST with a body)")
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "body field key=value (repeat); numbers, true, false and null stay JSON")
	cmd.Flags().StringVar(&input, "input", "", "send this file as the body, - for stdin")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm a DELETE")
	return cmd
}

func readInput(env Env, name string) ([]byte, error) {
	if name == "-" {
		if env.Stdin == nil {
			return nil, exit.Usagef("no input on stdin")
		}
		return io.ReadAll(env.Stdin)
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, exit.Usagef("cannot read input: %v", err)
	}
	return b, nil
}
