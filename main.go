// Command turnback records the changes AI coding agents make to a git
// working tree, one turn at a time, and lets you inspect or undo any single
// turn without touching the rest.
package main

import (
	"os"

	"github.com/Waiivyy/turnback/internal/cli"
)

func main() {
	os.Exit(cli.Main())
}
