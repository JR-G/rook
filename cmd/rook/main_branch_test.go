package main

import (
	"os"
	"testing"
)

func TestRunServeCommandConsumesPositionalCommand(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"rook", "serve", "-config", "missing.toml"}
	if err := run(); err == nil {
		t.Fatal("expected missing config to fail after consuming the serve command")
	}
}
