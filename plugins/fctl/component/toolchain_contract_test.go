package component

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestComponentToolchainVersionGuard(t *testing.T) {
	bin := t.TempDir()
	versions := map[string]string{
		"componentize-go": "componentize-go 0.4.1",
		"wasi-virt":       "wasi-virt 0.2.0",
		"wasm-tools":      "wasm-tools 1.239.0",
		"wasm-opt":        "wasm-opt version 124",
	}
	for name, version := range versions {
		path := filepath.Join(bin, name)
		contents := []byte("#!/bin/sh\nprintf '%s\\n' '" + version + "'\n")
		if err := os.WriteFile(path, contents, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	command := exec.Command("../scripts/check-component-toolchain.sh")
	command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("exact versions rejected: %v\n%s", err, output)
	}

	bad := filepath.Join(bin, "wasm-tools")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nprintf '%s\\n' 'wasm-tools 1.240.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("../scripts/check-component-toolchain.sh")
	command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("mismatched wasm-tools accepted:\n%s", output)
	}
}
