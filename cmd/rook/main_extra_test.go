package main

import (
	"bytes"
	"os"
	"testing"
)

func TestMainWritesErrorAndExits(t *testing.T) {
	oldArgs := os.Args
	oldExit := exitFn
	oldStderr := stderr
	defer func() {
		os.Args = oldArgs
		exitFn = oldExit
		stderr = oldStderr
	}()

	var output bytes.Buffer
	exitCode := 0
	stderr = &output
	exitFn = func(code int) {
		exitCode = code
	}
	os.Args = []string{"rook", "-config", "missing.toml"}

	main()

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if output.Len() == 0 {
		t.Fatal("expected stderr output from main")
	}
}

func TestRunFlagParseFailure(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"rook", "--bad-flag"}

	if err := run(); err == nil {
		t.Fatal("expected bad flag parse to fail")
	}
}
