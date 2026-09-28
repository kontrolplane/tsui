package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHelpGoesToStdout(t *testing.T) {
	if os.Getenv("TSUI_HELP_TEST") == "1" {
		os.Args = []string{programName, "--help"}
		Execute("dev", "none", "unknown")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelpGoesToStdout$")
	cmd.Env = append(os.Environ(), "TSUI_HELP_TEST=1", "NATS_PASSWORD=hunter2")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected --help to exit 0, got %v: %s", err, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"usage: tsui [flags]", "  -s, --server string", "  --context string", "  --tlsfirst", `(default "auto")`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected the help on stdout to contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  -server") || strings.Contains(out, "\n  -s ") {
		t.Errorf("expected the flags with two dashes and the shorthand next to its flag:\n%s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Error("expected defaults from the environment to stay out of the help")
	}
	if strings.Contains(stderr.String(), "usage") {
		t.Errorf("expected nothing about usage on stderr, got %q", stderr.String())
	}
}
