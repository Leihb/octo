package mswe

import "strings"

// ScopeFixPatch strips test-file sections from a `git diff`, leaving only the
// source changes. The Multi-SWE-bench harness applies the dataset's own hidden
// test patch on top of the model's fix_patch, so a model patch that also
// touched tests would conflict with — or worse, defeat — the hidden tests.
//
// The input is unified diff output where each file section starts with a
// `diff --git a/<path> b/<path>` header. Sections whose path is a Go test file
// (*_test.go) are dropped; the rest are rejoined unchanged.
func ScopeFixPatch(diff string) string {
	sections := splitDiffSections(diff)
	var kept []string
	for _, s := range sections {
		if isGoTestFile(diffSectionPath(s)) {
			continue
		}
		kept = append(kept, s)
	}
	return strings.Join(kept, "")
}

// splitDiffSections breaks a unified diff into per-file sections, each starting
// at a `diff --git ` line and including everything up to the next one.
func splitDiffSections(diff string) []string {
	lines := strings.SplitAfter(diff, "\n")
	var sections []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			sections = append(sections, cur.String())
			cur.Reset()
		}
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "diff --git ") {
			flush()
		}
		cur.WriteString(ln)
	}
	flush()
	return sections
}

// diffSectionPath extracts the b/ path from a section's `diff --git a/x b/y`
// header. Returns "" when the header is missing or malformed.
func diffSectionPath(section string) string {
	nl := strings.IndexByte(section, '\n')
	header := section
	if nl >= 0 {
		header = section[:nl]
	}
	if !strings.HasPrefix(header, "diff --git ") {
		return ""
	}
	fields := strings.Fields(strings.TrimPrefix(header, "diff --git "))
	if len(fields) != 2 {
		return ""
	}
	return strings.TrimPrefix(fields[1], "b/")
}

// isGoTestFile reports whether path is a Go test file. (The eval is Go-only;
// extend this set if the dataset language scope widens.)
func isGoTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}
