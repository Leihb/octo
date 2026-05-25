package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_NoArgs_PrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: octo") {
		t.Errorf("stdout missing usage banner; got: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty; got: %q", stderr.String())
	}
}

func TestRun_Version(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{arg}, strings.NewReader(""), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if !strings.HasPrefix(stdout.String(), "octo ") {
				t.Errorf("stdout should start with 'octo '; got: %q", stdout.String())
			}
		})
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr should mention 'unknown command'; got: %q", stderr.String())
	}
}
