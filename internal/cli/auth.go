package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/output"
	"github.com/nono/scut/internal/transport"
)

func newAuth(env Env) *cobra.Command {
	auth := Group("auth", "Store and check the API token")
	auth.AddCommand(newAuthLogin(env), newAuthStatus(env))
	return auth
}

func newAuthLogin(env Env) *cobra.Command {
	return &cobra.Command{
		Use:         "login",
		Short:       "Read a token from stdin and store it in the keyring",
		Args:        noArgs,
		Annotations: map[string]string{OpsAnnotation: "getWhoami"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := readToken(env, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			tok := strings.TrimSpace(string(b))
			scopes, workspace, err := config.ParseToken(tok)
			if err != nil {
				return err
			}
			profile := globalFlags(cmd)["profile"]
			if profile == "" {
				profile = env.Getenv("SCUT_PROFILE")
			}
			if profile == "" {
				profile = "default"
			}
			var opts []transport.Option
			if env.APIBase != "" {
				opts = append(opts, transport.WithBaseURL(env.APIBase))
			}
			opts = append(opts, transport.WithRoundTripper(env.Rec.API(nil)))
			d := transport.New(config.Settings{Profile: profile, Token: tok, Scopes: scopes, Workspace: workspace}, opts...)
			w, err := getWhoami(cmd.Context(), d)
			if err != nil {
				return err
			}
			if env.Keyring == nil {
				return exit.Usagef("no keyring available")
			}
			if err := env.Keyring.Set(config.KeyringService, profile, tok); err != nil {
				return &exit.Error{Code: exit.Other, Msg: "cannot store the token in the keyring: " + err.Error()}
			}
			return output.Print(cmd.OutOrStdout(), map[string]string{
				"workspace": workspace, "scopes": scopes, "member": w.Member.MentionName,
			}, false)
		},
	}
}

// readToken reads the token from stdin. On a terminal it prompts on stderr and
// turns off the echo.
func readToken(env Env, stderr io.Writer) ([]byte, error) {
	if env.StdinIsTTY {
		read := env.ReadPassword
		if read == nil {
			read = term.ReadPassword
		}
		_, _ = fmt.Fprint(stderr, "Token: ")
		b, err := read(env.StdinFd)
		_, _ = fmt.Fprintln(stderr)
		if err != nil {
			return nil, &exit.Error{Code: exit.Other, Msg: "cannot read the token: " + err.Error()}
		}
		return b, nil
	}
	if env.Stdin == nil {
		return nil, exit.Usagef("no token on stdin")
	}
	return io.ReadAll(env.Stdin)
}

func newAuthStatus(env Env) *cobra.Command {
	return &cobra.Command{
		Use:         "status",
		Short:       "Show the workspace, scopes and source of the token",
		Args:        noArgs,
		Annotations: map[string]string{OpsAnnotation: "getWhoami"},
		RunE: withDeps(env, func(cmd *cobra.Command, _ []string, d *deps) error {
			w, err := getWhoami(cmd.Context(), d.Doer)
			if err != nil {
				return err
			}
			return d.Out(map[string]string{
				"workspace": d.Settings.Workspace, "scopes": d.Settings.Scopes,
				"source": d.Settings.TokenSource, "member": w.Member.MentionName,
			})
		}),
	}
}
