package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/exit"
	"github.com/nono/scut/internal/output"
	"github.com/nono/scut/internal/transport"
)

const keyringService = "scut"

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
			if env.Stdin == nil {
				return exit.Usagef("no token on stdin")
			}
			b, err := io.ReadAll(env.Stdin)
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
			d := transport.New(config.Settings{Profile: profile, Token: tok, Scopes: scopes, Workspace: workspace}, opts...)
			w, err := getWhoami(cmd.Context(), d)
			if err != nil {
				return err
			}
			if env.Keyring == nil {
				return exit.Usagef("no keyring available")
			}
			if err := env.Keyring.Set(keyringService, profile, tok); err != nil {
				return &exit.Error{Code: exit.Other, Msg: "cannot store the token in the keyring: " + err.Error()}
			}
			return output.Print(cmd.OutOrStdout(), map[string]string{
				"workspace": workspace, "scopes": scopes, "member": w.Member.MentionName,
			}, false)
		},
	}
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
