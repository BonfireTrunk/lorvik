// Command lorvik is the Lorvik command-line client for humans and agents.
package main

import (
	"os"

	"github.com/BonfireTrunk/lorvik/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
