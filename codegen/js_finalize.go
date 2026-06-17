package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const jsBakeTSConfig = `{
  "compilerOptions": {
    "target": "ES2020",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "declaration": true,
    "strict": true,
    "skipLibCheck": true
  },
  "include": ["./**/*.ts"]
}
`

// compileJSIntegration runs tsc on generated .ts in outDir, then removes intermediate .ts
// and the temporary tsconfig so only .js and .d.ts remain.
func compileJSIntegration(projectRoot, outDir string) error {
	cfgPath := filepath.Join(outDir, "tsconfig.golem-bake.json")
	if err := os.WriteFile(cfgPath, []byte(jsBakeTSConfig), 0o644); err != nil {
		return fmt.Errorf("write tsconfig.golem-bake.json: %w", err)
	}

	tscJS := filepath.Join(projectRoot, "node_modules", "typescript", "lib", "tsc.js")
	var cmd *exec.Cmd
	if _, err := os.Stat(tscJS); err == nil {
		node, err := exec.LookPath("node")
		if err != nil {
			return fmt.Errorf("js-client integration: need Node.js to run TypeScript compiler: %w", err)
		}
		cmd = exec.Command(node, tscJS, "-p", cfgPath)
	} else {
		tscBin, err := exec.LookPath("tsc")
		if err != nil {
			return fmt.Errorf("js-client integration: install devDependency \"typescript\" in your project root, or put \"tsc\" on PATH")
		}
		cmd = exec.Command(tscBin, "-p", cfgPath)
	}
	cmd.Dir = outDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tsc in %s: %w\n%s", outDir, err, out)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", outDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "tsconfig.golem-bake.json" {
			_ = os.Remove(filepath.Join(outDir, name))
			continue
		}
		if !strings.HasSuffix(name, ".ts") {
			continue
		}
		switch {
		case name == "EntityManager.ts",
			name == "entities_pb.ts",
			name == "client.ts",
			strings.HasSuffix(name, "Synced.ts"):
			_ = os.Remove(filepath.Join(outDir, name))
		}
	}
	return nil
}
