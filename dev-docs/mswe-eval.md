# Multi-SWE-bench eval harness

Measures octo's task-completion quality on a small slice of **Multi-SWE-bench**
(Go subset) — real GitHub issues in real repos, judged by hidden tests. This is
the Tier-1 "eval / regression" capability: it's how you tell whether a prompt,
tool, or model change made octo *better or worse* at actually fixing issues.

## Why it's not in CI

A real eval **must** use the real model (it measures whether the fix works) and
the official judge needs **Docker** + the `multi_swe_bench` Python package +
network (clone repos, pull images). None of that belongs in `go test` (the
project rule: no live network in CI). So this is a **manual / periodic** run.

The Go code that's part of CI is only the pure logic in `internal/mswe`
(dataset parsing, prediction writing, patch scoping, config, report parsing) —
fully unit-tested. The orchestration in `cmd/mswe-eval` is exercised by the
real run below.

## Architecture (two stages)

```
mswe-eval generate  (our Go tool, drives octo)
  for each Go instance:
    git clone <repo> ; git checkout <base_commit>
    octo chat --tools --permission-mode strict --no-save "<issue>"
    git add -A ; git diff --cached ; strip *_test.go hunks
  → predictions.jsonl   {org, repo, number, fix_patch}

mswe-eval judge  (invokes the official harness)
  → config.json {patch_files, dataset_files, output_dir, ...}
  → python -m multi_swe_bench.harness.run_evaluation --config config.json
       (builds a Docker env per instance, applies fix_patch + hidden tests, runs)
  → final_report.json → resolved / total
```

octo only ever produces the source patch; the official harness owns the
Docker-based judging. octo runs with an **isolated `HOME`** (a throwaway
`<workdir>/home`) so eval sessions/memory don't touch your real `~/.octo`, and a
permissive `permissions.yml` there lets it run tools without prompts in the
disposable clone.

## Prerequisites

- A model key in the environment (e.g. `ANTHROPIC_API_KEY` + `ANTHROPIC_BASE_URL`
  for a Kimi/Anthropic-compatible endpoint). `generate` passes the environment
  through to octo.
- `git`, network access (clone repos).
- For `judge`: **Docker running**, `python3`, and `pip install multi_swe_bench`.

## Get the Go dataset slice

The dataset lives on HuggingFace as raw JSONL: `ByteDance-Seed/Multi-SWE-bench`
(or the smaller `…_mini` / `…-flash`). Download it and keep only Go records, e.g.

```bash
huggingface-cli download ByteDance-Seed/Multi-SWE-bench --repo-type dataset --local-dir mswe-data
# then filter to Go (field name confirmed by `inspect`, usually "language":"go")
# into a single file, e.g. mswe-data/go.jsonl
```

`--dataset` (our tool) and `dataset_files` (the harness config) both point at
this same Go JSONL.

## Run it

```bash
# 0. Confirm the real schema FIRST — the dataset is raw JSONL and field names
#    can drift by release. Check that base_commit + problem appear non-empty.
make eval-mswe-inspect DATASET=mswe-data/go.jsonl LIMIT=1

# 1. Full run: generate patches with octo, then judge (5 instances by default).
make eval-mswe DATASET=mswe-data/go.jsonl LIMIT=5
```

Or drive the stages directly:

```bash
./mswe-eval generate --dataset mswe-data/go.jsonl --limit 5 --octo ./octo --out predictions.jsonl
./mswe-eval judge    --dataset mswe-data/go.jsonl --predictions predictions.jsonl
```

## Confirm on the first run (build-time unknowns)

The scaffold is tolerant but a few things can only be pinned against the real
data / installed harness. Check these on the first run and adjust if needed:

1. **Record field names** — `inspect` prints each record's keys. The accessors
   in `internal/mswe/instance.go` try `base_commit`/`base.sha` and
   `problem_statement`/`resolved_issues`; if `inspect` shows different names,
   extend those accessors.
2. **Harness config keys** — `internal/mswe/config.go` writes the documented
   keys (`patch_files`, `dataset_files`, `output_dir`, worker counts). If your
   `multi_swe_bench` version wants more/different keys, the run will say so;
   update `HarnessConfig`.
3. **Report path/shape** — `judge` searches for `final_report.json` under the
   output dir and `ParseReport` accepts either ID-lists or counts for
   `resolved_instances`/`unresolved_instances`. Adjust if the real report
   differs.

## Cost & scale

Each instance is one full octo agentic run (cheap on Kimi) plus one Docker
build + test run (slow, heavy). Start at `LIMIT=5`, grow once the pipeline is
proven. Expect the first run to be dominated by Docker image builds.
