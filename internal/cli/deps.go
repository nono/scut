package cli

import (
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/nono/scut/internal/cache"
	"github.com/nono/scut/internal/config"
	"github.com/nono/scut/internal/output"
	"github.com/nono/scut/internal/resolve"
	"github.com/nono/scut/internal/transport"
)

const cacheTTL = time.Hour

// deps is what a command that calls the API needs.
type deps struct {
	Settings config.Settings
	Doer     *transport.Doer
	Resolver *resolve.R
	Out      func(v any) error
	Clock    transport.Clock
}

// globalFlags returns the values of the root flags, in the keys of config.Options.Flags.
func globalFlags(cmd *cobra.Command) map[string]string {
	f := map[string]string{}
	if v, _ := cmd.Flags().GetBool("human"); v {
		f["output"] = "human"
	}
	if v, _ := cmd.Flags().GetString("token-file"); v != "" {
		f["token-file"] = v
	}
	if v, _ := cmd.Flags().GetString("profile"); v != "" {
		f["profile"] = v
	}
	return f
}

// newDeps loads the settings and builds the clients. Only commands that call
// the API use it, so --help and usage work without a token.
func newDeps(cmd *cobra.Command, env Env) (*deps, error) {
	s, err := config.Load(config.Options{
		Flags:   globalFlags(cmd),
		Getenv:  env.Getenv,
		Dir:     env.Dir,
		Keyring: env.Keyring,
	})
	if err != nil {
		return nil, err
	}
	var opts []transport.Option
	if env.APIBase != "" {
		opts = append(opts, transport.WithBaseURL(env.APIBase))
	}
	now := env.Now
	if now == nil {
		now = time.Now
	}
	opts = append(opts, transport.WithClock(now))
	if env.Sleep != nil {
		opts = append(opts, transport.WithSleep(env.Sleep))
	}
	opts = append(opts, transport.WithHTTPClient(&http.Client{Transport: env.Rec.API(nil)}))
	d := transport.New(s, opts...)
	c := cache.New(s.CacheDir, cacheTTL, time.Now)
	human := s.Output == "human"
	w := cmd.OutOrStdout()
	return &deps{
		Settings: s,
		Doer:     d,
		Resolver: resolve.New(d, c).WithContext(cmd.Context()),
		Out:      func(v any) error { return output.Print(w, v, human) },
		Clock:    transport.Clock{Now: now, Sleep: env.Sleep},
	}, nil
}

// withDeps wraps a command body so that it gets deps, built before the body runs.
func withDeps(env Env, run func(cmd *cobra.Command, args []string, d *deps) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		d, err := newDeps(cmd, env)
		if err != nil {
			return err
		}
		return run(cmd, args, d)
	}
}
