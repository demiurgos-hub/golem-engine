// Command golem-bake generates Golem Engine integration code.
// entity sync code from golem.yaml (entity_schema + command_schema dirs, etc.).
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/demiurgos-hub/golem-engine/codegen"
)

func main() {
	remove := len(os.Args) == 3 && os.Args[1] == "--remove-integration"
	if len(os.Args) != 1 && !remove {
		fmt.Fprintln(os.Stderr, "usage: golem-bake [--remove-integration shared-only-target]")
		os.Exit(1)
	}

	root, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	if remove {
		if err := codegen.RemoveIntegration(root, os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := codegen.Bake(root); err != nil {
		log.Fatal(err)
	}
}
