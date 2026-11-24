// Command golem-bake generates Golem Engine integration code (protobuf schema,
// entity sync code and client bindings) from golem.yaml in the current directory.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/demiurgos-hub/golem-engine/codegen"
)

// Exit codes. Usage and configuration problems use exitUsage so callers of
// --check can tell "generated code is stale" (exitFailure) from "check could not run".
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `usage:
  golem-bake                                  generate code for the project in the current directory
  golem-bake --check                          verify generated code is up to date without writing
  golem-bake --remove-integration <target>    remove a configured shared-only bridge's generated files

--check renders exactly what golem-bake would write (same templates, gofmt and
TypeScript toolchain) into a temporary directory and compares it with the
project. Text is compared after converting CRLF to LF on both sides, so
autocrlf checkouts are not reported stale. It lists each differing path as
missing, stale or unexpected (a top-level file in an integration output
directory with the Golem generated header that a bake would no longer write).

Exit codes: 0 success or up to date; 1 bake/removal failed or --check found
differences; 2 usage or configuration error (including any --check failure
other than differences).
`

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "golem-bake: getwd: %v\n", err)
		os.Exit(exitUsage)
	}
	os.Exit(run(os.Args[1:], root, os.Stdout, os.Stderr))
}

// run executes one golem-bake invocation against root and returns its exit code.
func run(args []string, root string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0:
		if err := codegen.Bake(root); err != nil {
			fmt.Fprintf(stderr, "golem-bake: %v\n", err)
			return exitFailure
		}
		return exitOK
	case len(args) == 1 && (args[0] == "-h" || args[0] == "--help"):
		fmt.Fprint(stdout, usage)
		return exitOK
	case len(args) == 1 && args[0] == "--check":
		return runCheck(root, stdout, stderr)
	case len(args) == 2 && args[0] == "--remove-integration":
		if err := codegen.RemoveIntegration(root, args[1]); err != nil {
			fmt.Fprintf(stderr, "golem-bake: %v\n", err)
			return exitFailure
		}
		return exitOK
	}
	for _, arg := range args {
		if arg == "--check" {
			fmt.Fprintln(stderr, "golem-bake: --check takes no other arguments")
			fmt.Fprint(stderr, usage)
			return exitUsage
		}
	}
	fmt.Fprintf(stderr, "golem-bake: unrecognized arguments %q\n", args)
	fmt.Fprint(stderr, usage)
	return exitUsage
}

func runCheck(root string, stdout, stderr io.Writer) int {
	result, err := codegen.Check(root)
	if err != nil {
		fmt.Fprintf(stderr, "golem-bake --check: %v\n", err)
		return exitUsage
	}
	if result.UpToDate() {
		fmt.Fprintf(stdout, "golem-bake: generated code is up to date (%d files)\n", result.Files)
		return exitOK
	}
	fmt.Fprintf(stdout, "golem-bake: generated code is out of date (%d difference(s)):\n", len(result.Drift))
	for _, drift := range result.Drift {
		fmt.Fprintf(stdout, "  %-10s  %s\n", drift.Kind, drift.Path)
	}
	fmt.Fprintln(stdout, "Run golem-bake in the project root to regenerate.")
	return exitFailure
}
