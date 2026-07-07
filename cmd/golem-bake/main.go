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
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: golem-bake")
		os.Exit(1)
	}

	root, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	if err := codegen.Bake(root); err != nil {
		log.Fatal(err)
	}
}
