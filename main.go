// Command goblin manages isolated, per-project package environments.
package main

import (
	"os"

	"goblin/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
