// Command scut is a command line client for the Shortcut REST API v4.
package main

import (
	"os"

	"github.com/nono/scut/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr, cli.OSEnv()))
}
