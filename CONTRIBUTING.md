# Contributing to Octo

Thanks for taking the time to contribute. Every PR will be reviewed. We evaluate
each contribution along three dimensions:

1. **Value of the need** — is this useful, and to whom?
2. **Architectural impact** — does it fit the existing design?
3. **Code standards** — does it meet our quality bar?

Read the sections below before opening a PR. If your contribution clearly
delivers outsized value, the rules here can bend — see [Exceptions](#exceptions).

---

## 1. Architecture First

Improvements built on top of the existing, stable architecture are accepted
quickly. By "stable architecture" we mean a change that:

- Solves the need with the **smallest possible diff**.
- **Adds no new configuration knobs** unless strictly required.
- **Adds no new dependencies** unless strictly required (see also §3).
- **Respects the existing design intent** — same layering, same abstractions,
  same naming conventions.
- Ideally **simplifies** the architecture rather than expanding it.

PRs that introduce parallel mechanisms, speculative abstractions, or "just in
case" flexibility will be sent back for trimming.

## 2. Needs Should Be Shared and Side-Effect-Free

We prefer changes that benefit **most users** and have **no side effects** on
others.

- **Common needs** (broadly applicable, opt-in by nature, isolated blast
  radius) → fast track.
- **Niche needs** (valuable to a few, but with potential to affect others'
  workflows, performance, or defaults) → reviewed more cautiously. Expect
  questions about scope, defaults, and rollout.

If your change alters existing default behavior, call it out explicitly in the
PR description.

## 3. Code Standards

### Tests

- All tests **must pass** before a PR can be merged.
- **Coverage must not drop.** New code needs new tests.

### Commits & PRs

- **Write commit messages and PR titles/descriptions in English.** This applies
  to everyone, regardless of working language.
- Keep commits focused; squash noise before requesting review.
- PR descriptions should briefly state: what, why, and any user-visible impact.

### Built with Octo

- PRs **authored using Octo itself** are prioritized for review and
  merge. Mention it in the PR description if applicable. We dogfood our own
  tool.

### Dependencies

- **Avoid adding new libraries.** Prefer the standard library, existing
  dependencies, or a few lines of code over pulling in another gem/package.
- If a new dependency is genuinely necessary, justify it in the PR description:
  why this library, why not write it ourselves, license, maintenance status.

### Style

- Follow the conventions already present in the file you're editing.
- See each sub-project's `.octorules` for project-specific rules
  (`octo/`, `platform/`, `installer/`).

---

## 4. Branch & merge workflow

`main` is protected. **No one — including maintainers — pushes directly to `main`.** Every change reaches `main` through a pull request.

- Pull the latest `main` (`git pull --ff-only origin main`) and create a topic branch off it. Use a descriptive name with a type prefix: `fix/...`, `feat/...`, `chore/...`, `docs/...`.
- Commit on the topic branch only. Push the branch to `origin` and open a PR against `main`.
- CI must be **green on the Go matrix** (1.22 / 1.23 × Linux / macOS / Windows) before the PR can merge.
- Your branch must be **up to date with `main`** before merging — rebase or merge the latest `main` in if it has moved.
- Force-pushes to `main` and deletion of `main` are blocked at the GitHub level.

If you find yourself with local commits on `main`, move them onto a topic branch before pushing:

```sh
git switch -c fix/your-change           # rename current HEAD to a topic branch
git switch main && git reset --hard origin/main   # restore main to upstream
git switch fix/your-change              # back on the topic branch, push from here
```

---

## 5. Go development (work-in-progress)

`main` is currently in transition: the Ruby tree under `lib/`, `scripts/`, `spec/` is frozen, and a Go rewrite is growing alongside it under `cmd/` and `internal/`. New non-bugfix work should target the Go side.

Setup:

```sh
# Need Go 1.22+
go version

# Run the standard checks the CI runs:
make fmt-check    # fail if anything would be reformatted by `gofmt`
make vet          # go vet ./...
make test         # go test -race ./...
make build        # produces ./octo with version metadata baked in
```

Project layout:

- `cmd/octo/`              binary entrypoint
- `internal/version/`      build-time version metadata (overridden via `-ldflags` for releases)
- `internal/agent/`        agent core (M2)
- `internal/provider/`     LLM provider clients (M2)
- `internal/tools/`        built-in tools (M2)
- `internal/skills/`       skill loader (M3)
- `internal/server/`       HTTP + WebSocket server for the Web UI (M3)

`gofmt` and `go vet` cleanliness are mandatory. `golangci-lint` is not yet wired up but PRs are welcome to follow [Effective Go](https://go.dev/doc/effective_go) and the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments) guide.

Ruby fixes (only critical regressions) should target the `archive/ruby` branch directly, not `main`.

---

## Exceptions

Rules exist to keep the project healthy, not to block valuable work. For
contributions that deliver **substantial, clear value**, the standards above
can be relaxed at the maintainers' discretion. When in doubt, open an issue or
draft PR first to discuss the trade-offs.

---

## Code of Conduct

Participation in this project is governed by the
[Code of Conduct](./CODE_OF_CONDUCT.md). By contributing, you agree to uphold
it.
