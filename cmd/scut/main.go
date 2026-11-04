// Command scut is a command line client for the Shortcut REST API v4.
package main

import (
	"os"

	"github.com/nono/scut/internal/cli"
	"github.com/nono/scut/internal/transport"
)

// version is set at release time with -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	transport.Version = version
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr, cli.OSEnv()))
}
