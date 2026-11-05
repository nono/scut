package cli

import (
	"github.com/spf13/cobra"

	"github.com/nono/scut/api"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/output"
	"github.com/nono/scut/internal/spec"
)

func newSpec() *cobra.Command {
	var depth int
	cmd := &cobra.Command{
		Use:   "spec <operationId|SchemaName>",
		Short: "Show the shape of a v4 operation or schema",
		Long: "Show the method, path, parameters, body and 2xx response of an operation,\n" +
			"or one schema, from the v4 spec. A \"?\" suffix marks an optional key.\n" +
			"\"$Name\" is a schema below --depth: run scut spec Name to see it.\n" +
			"The path is in the form that scut api takes. No token is needed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if depth < 0 {
				return exit.Usagef("--depth must be 0 or more")
			}
			s, err := spec.Parse(api.Spec)
			if err != nil {
				return err
			}
			v, ok := s.Lookup(args[0], depth)
			if !ok {
				return exit.NotFoundf("no operation or schema %q in the v4 spec", args[0])
			}
			human, _ := cmd.Flags().GetBool("human")
			return output.Print(cmd.OutOrStdout(), v, human)
		},
	}
	cmd.Flags().IntVar(&depth, "depth", 2, "levels of named schemas to show inline")
	return cmd
}
