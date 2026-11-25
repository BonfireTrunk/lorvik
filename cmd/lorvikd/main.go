// Command lorvikd is the Lorvik server.
package main

import (
	"os"

	"github.com/BonfireTrunk/lorvik/internal/lorvikd"
)

func main() {
	os.Exit(lorvikd.Main(os.Args[1:], os.Stdout, os.Stderr))
}
