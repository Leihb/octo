package memory

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// gitAuthorEmail / gitAuthorName are the committer identity used for octo's
// auto-commits. Local-repo config so we don't perturb the user's global
// .gitconfig.
const (
	gitAuthorEmail = "memory@octo.local"
	gitAuthorName  = "octo memory"
)

// gitAvailableOnce caches the result of "is `git` on PATH?". Looked up once
// per process — the answer doesn't change at runtime.
var (
	gitAvailableOnce sync.Once
	gitAvailableVal  bool
)

// gitAvailable reports whether `git` is on PATH. Cached. When false, Store
// methods that would have committed silently skip — memory still works, just
// without an audit trail.
func gitAvailable() bool {
	gitAvailableOnce.Do(func() {
		_, err := exec.LookPath("git")
		gitAvailableVal = err == nil
	})
	return gitAvailableVal
}

// isGitRepo returns true if dir/.git exists (file or directory; submodule-style
// .git files count too).
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// runGit runs `git <args...>` inside dir, returning trimmed stdout and a
// wrapped error that includes stderr when the command fails.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Make output deterministic across user environments / locales.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// gitInit initializes dir as a git repo (idempotent) and sets local
// user.email / user.name so commits don't depend on the user's global config.
// Initial branch is `main` to match the project default.
func gitInit(dir string) error {
	if !gitAvailable() {
		return errInternalGitMissing
	}
	if isGitRepo(dir) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// -b main is supported from git 2.28+. Fall back to `git init` + a branch
	// rename if it errors (older runners).
	if _, err := runGit(dir, "init", "-b", "main"); err != nil {
		if _, err2 := runGit(dir, "init"); err2 != nil {
			return err2
		}
		_, _ = runGit(dir, "symbolic-ref", "HEAD", "refs/heads/main")
	}
	if _, err := runGit(dir, "config", "user.email", gitAuthorEmail); err != nil {
		return err
	}
	if _, err := runGit(dir, "config", "user.name", gitAuthorName); err != nil {
		return err
	}
	// Drop a .gitignore so runtime / transient files (the cross-process lock,
	// the per-session state file) don't pollute the audit trail. The user-
	// facing memory artifacts (entries, MEMORY.md, memory_summary.md) stay
	// tracked.
	gi := []byte(".lock\n.state\n")
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), gi, 0o644)
	return nil
}

// gitHead returns the current HEAD SHA, or "" if the repo has no commits yet
// (the typical state right after gitInit).
func gitHead(dir string) (string, error) {
	out, err := runGit(dir, "rev-parse", "HEAD")
	if err != nil {
		// "fatal: ambiguous argument 'HEAD'" means no commits yet — not an error
		// for our purposes.
		if strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "ambiguous argument") {
			return "", nil
		}
		return "", err
	}
	return out, nil
}

// gitCommit stages every change under dir and commits with the given message.
// Returns the new HEAD SHA, or "" with no error if the working tree was clean
// (nothing to commit is a normal no-op, not a failure).
func gitCommit(dir, message string) (string, error) {
	if !gitAvailable() || !isGitRepo(dir) {
		return "", nil
	}
	if _, err := runGit(dir, "add", "-A"); err != nil {
		return "", err
	}
	// `--allow-empty=false` is the default but be explicit. If nothing is
	// staged after `add -A`, the commit errors with "nothing to commit" — we
	// treat that as a no-op.
	if _, err := runGit(dir, "commit", "-m", message); err != nil {
		if strings.Contains(err.Error(), "nothing to commit") || strings.Contains(err.Error(), "no changes added") {
			return "", nil
		}
		return "", err
	}
	return gitHead(dir)
}

// gitListAllPaths returns the union of every distinct path that has ever
// appeared in the history under dir (including paths that are currently
// deleted). Used by ListArchived to discover entries removed by past
// consolidate-drop commits.
func gitListAllPaths(dir string) ([]string, error) {
	if !gitAvailable() || !isGitRepo(dir) {
		return nil, nil
	}
	out, err := runGit(dir, "log", "--all", "--pretty=format:", "--name-only")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		paths = append(paths, line)
	}
	return paths, nil
}

// gitLastTouching returns the SHA of the most recent commit that modified
// path, or "" if no commit ever touched it.
func gitLastTouching(dir, path string) (string, error) {
	if !gitAvailable() || !isGitRepo(dir) {
		return "", nil
	}
	out, err := runGit(dir, "log", "-1", "--pretty=format:%H", "--", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// gitShow returns the contents of path at ref. Used to recover the content of
// a file that no longer exists in the working tree.
func gitShow(dir, ref, path string) (string, error) {
	if !gitAvailable() || !isGitRepo(dir) {
		return "", errInternalGitMissing
	}
	return runGit(dir, "show", ref+":"+path)
}

// gitDiff returns the unified diff between two refs (or between a ref and the
// working tree if head is empty). Used by the future sub-agent consolidator
// (#6) to see what changed since the last consolidation baseline.
func gitDiff(dir, base, head string) (string, error) {
	if !gitAvailable() || !isGitRepo(dir) {
		return "", errInternalGitMissing
	}
	args := []string{"diff"}
	if head == "" {
		args = append(args, base)
	} else {
		args = append(args, base+".."+head)
	}
	return runGit(dir, args...)
}

// errInternalGitMissing is returned by helpers that require git when it isn't
// available or the dir isn't a repo. Callers higher up convert it to a no-op
// (e.g. ListArchived returns nil, nil rather than surfacing this).
var errInternalGitMissing = errors.New("memory: git unavailable")
