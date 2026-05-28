package main

import "testing"

func TestResolveVerbosity_FlagsBeatEnv(t *testing.T) {
	t.Setenv("OCTO_VERBOSITY", "verbose")
	if got := resolveVerbosity(true, false); got != verbosityQuiet {
		t.Errorf("--quiet must beat OCTO_VERBOSITY=verbose; got %v", got)
	}
	if got := resolveVerbosity(false, true); got != verbosityVerbose {
		t.Errorf("--verbose with env verbose should still be verbose; got %v", got)
	}
}

func TestResolveVerbosity_QuietWinsOverVerbose(t *testing.T) {
	// Both flags set together — quiet wins by documented precedence.
	if got := resolveVerbosity(true, true); got != verbosityQuiet {
		t.Errorf("--quiet --verbose → expected quiet, got %v", got)
	}
}

func TestResolveVerbosity_EnvFallback(t *testing.T) {
	cases := map[string]verbosity{
		"":          verbosityNormal,
		"normal":    verbosityNormal,
		"  quiet  ": verbosityQuiet,
		"QUIET":     verbosityQuiet,
		"verbose":   verbosityVerbose,
		"Verbose":   verbosityVerbose,
		"something": verbosityNormal,
		"   ":       verbosityNormal,
	}
	for envVal, want := range cases {
		t.Run(envVal, func(t *testing.T) {
			t.Setenv("OCTO_VERBOSITY", envVal)
			if got := resolveVerbosity(false, false); got != want {
				t.Errorf("OCTO_VERBOSITY=%q → %v, want %v", envVal, got, want)
			}
		})
	}
}

func TestVerbosity_Accessors(t *testing.T) {
	if !verbosityQuiet.quiet() || verbosityQuiet.verbose() {
		t.Error("verbosityQuiet should be quiet, not verbose")
	}
	if verbosityNormal.quiet() || verbosityNormal.verbose() {
		t.Error("verbosityNormal should be neither")
	}
	if verbosityVerbose.quiet() || !verbosityVerbose.verbose() {
		t.Error("verbosityVerbose should be verbose, not quiet")
	}
}
