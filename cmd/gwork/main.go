// Command gwork is a read-only CLI and MCP server for Gmail, Google Drive,
// Google Chat and Google Calendar.
package main

import (
	"os"

	"github.com/digio/gwork-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
