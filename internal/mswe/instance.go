// Package mswe holds the pure, testable pieces of the Multi-SWE-bench eval
// harness: dataset parsing, prediction writing, fix-patch scoping, harness
// config generation, and report parsing. The shell-out orchestration (clone a
// repo, drive octo, invoke the Python+docker judge) lives in cmd/mswe-eval.
//
// Multi-SWE-bench ships as raw JSONL and the exact record field names can drift
// by release, so dataset access goes through tolerant accessors with fallbacks
// rather than a rigid struct. Confirm the real names with `mswe-eval inspect`
// before trusting a full run.
package mswe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Instance is one benchmark record, kept as the raw decoded object so accessors
// can tolerate schema variation and `inspect` can enumerate every key.
type Instance struct {
	raw map[string]any
}

// LoadInstances reads newline-delimited JSON records, optionally filtered to a
// language (case-insensitive; "" = no filter) and capped at limit (<=0 = all).
func LoadInstances(r io.Reader, language string, limit int) ([]Instance, error) {
	sc := bufio.NewScanner(r)
	// Records carry full patches; allow large lines (up to 16 MiB).
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var out []Instance
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, fmt.Errorf("mswe: parse record: %w", err)
		}
		inst := Instance{raw: m}
		if language != "" && !strings.EqualFold(inst.Language(), language) {
			continue
		}
		out = append(out, inst)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mswe: read dataset: %w", err)
	}
	return out, nil
}

// Keys returns the record's top-level field names, sorted — used by `inspect`
// to confirm the real schema against this package's accessors.
func (i Instance) Keys() []string {
	ks := make([]string, 0, len(i.raw))
	for k := range i.raw {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Raw exposes the underlying decoded object (for inspect to print scalars).
func (i Instance) Raw() map[string]any { return i.raw }

func (i Instance) Org() string      { return str(i.raw, "org") }
func (i Instance) Repo() string     { return str(i.raw, "repo") }
func (i Instance) Number() int      { return num(i.raw, "number", "pull_number", "pr") }
func (i Instance) Language() string { return str(i.raw, "language", "lang") }

// ID is a human-readable instance identifier (org/repo#number).
func (i Instance) ID() string {
	return fmt.Sprintf("%s/%s#%d", i.Org(), i.Repo(), i.Number())
}

// CloneURL is the GitHub URL the runner clones to set up the working tree.
func (i Instance) CloneURL() string {
	return fmt.Sprintf("https://github.com/%s/%s.git", i.Org(), i.Repo())
}

// BaseCommit is the SHA the repo is checked out at before octo runs. Tries a
// flat "base_commit" first, then a nested base.sha (SWE-bench-style records).
func (i Instance) BaseCommit() string {
	if s := str(i.raw, "base_commit", "base_sha"); s != "" {
		return s
	}
	if b, ok := i.raw["base"].(map[string]any); ok {
		return str(b, "sha", "commit")
	}
	return ""
}

// ProblemStatement is the issue/PR text given to octo as the task. Tries the
// common flat fields, then falls back to concatenating resolved_issues titles
// and bodies.
func (i Instance) ProblemStatement() string {
	if s := str(i.raw, "problem_statement", "problem", "issue"); s != "" {
		return s
	}
	var b strings.Builder
	if issues, ok := i.raw["resolved_issues"].([]any); ok {
		for _, it := range issues {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if t := str(m, "title"); t != "" {
				b.WriteString(t)
				b.WriteString("\n\n")
			}
			if body := str(m, "body"); body != "" {
				b.WriteString(body)
				b.WriteString("\n\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// str returns the first key present as a non-empty string.
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// num returns the first key present as an int (JSON numbers decode to float64).
func num(m map[string]any, keys ...string) int {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case string:
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n
			}
		}
	}
	return 0
}
