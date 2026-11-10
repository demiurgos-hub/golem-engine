// Command golem-nav-bake bakes explicit server navigation geometry without
// changing golem-bake's schema/code-generation responsibilities.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/demiurgos-hub/golem-engine/golem/nav/navmesh/navbuild"
)

func main() {
	input := flag.String("in", "", "explicit server navigation source JSON")
	output := flag.String("out", "", "portable navigation artifact")
	flag.Parse()
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "usage: golem-nav-bake -in world.navigation.json -out world.nav")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	result, err := navbuild.BakeFile(ctx, *input, *output)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result.Diagnostics); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
