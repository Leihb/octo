package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/taskgraph"
)

// runTask handles `octo task <subcommand>`. PR2 (this file) wires only
// `start "<goal>"` — it plans the DAG via the LLM and persists to
// ~/.octo/tasks/<id>.json. The scheduler (`run`), inspection
// (`list / status / show`), and lifecycle (`resume / cancel`) commands
// land in subsequent PRs.
func runTask(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTaskUsage(stdout)
		return 2
	}

	switch args[0] {
	case "start":
		return runTaskStart(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		printTaskUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "octo task: unknown subcommand %q\n", args[0])
		printTaskUsage(stderr)
		return 2
	}
}

func printTaskUsage(w io.Writer) {
	fmt.Fprintln(w, "octo task — autonomous task orchestration (M11)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: octo task <subcommand> [args...]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  start \"<goal>\"   Decompose a goal into a subtask DAG and persist it")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Coming in later PRs: run, list, status, show, resume, cancel.")
}

// runTaskStart handles `octo task start "<goal>" [flags]`. It runs the
// planner side-call against the same provider chain as `octo chat`, then
// persists the resulting DAG. The scheduler isn't wired yet — this PR's
// end state is a `pending` task on disk that a later `octo task run <id>`
// (PR3) will execute.
func runTaskStart(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	providerName := fs.String("provider", providerAnthropic, "Provider: anthropic | openai")
	model := fs.String("model", "", "Model name (defaults to the provider's cheapest reasoning model)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: octo task start \"<goal>\" [--provider …] [--model …]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	goal := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if goal == "" {
		fmt.Fprintln(stderr, "octo task start: a goal is required (e.g. octo task start \"migrate the auth middleware\")")
		return 2
	}

	resolvedModel := *model
	if resolvedModel == "" {
		resolvedModel = defaultModels[*providerName]
	}
	if resolvedModel == "" {
		fmt.Fprintf(stderr, "octo task start: unknown provider %q (use 'anthropic' or 'openai')\n", *providerName)
		return 2
	}

	prov, err := buildProvider(*providerName, stderr)
	if err != nil {
		return 1
	}

	a := agent.New(providerSender{
		p:        prov,
		cacheKey: newCacheKey(),
	}, resolvedModel)
	a.MaxTokens = defaultMaxTokensForPlanner

	fmt.Fprintf(stdout, "Planning…  goal: %s\n", oneLine(goal))
	res, err := a.PlanTask(context.Background(), goal)
	if err != nil {
		fmt.Fprintf(stderr, "octo task start: planner: %v\n", err)
		return 1
	}
	if len(res.Subtasks) == 0 {
		fmt.Fprintln(stderr, "octo task start: planner returned no subtasks — refine the goal and try again")
		return 1
	}

	subs := make([]taskgraph.Subtask, 0, len(res.Subtasks))
	for i, ps := range res.Subtasks {
		subs = append(subs, taskgraph.Subtask{
			ID:          i + 1,
			Description: ps.Description,
			BlockedBy:   ps.BlockedBy,
			Status:      taskgraph.SubtaskPending,
		})
	}

	store, err := taskgraph.NewStore()
	if err != nil {
		fmt.Fprintf(stderr, "octo task start: %v\n", err)
		return 1
	}
	task, err := store.Create(goal, subs)
	if err != nil {
		fmt.Fprintf(stderr, "octo task start: persist: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Created task %s\n\n", task.ID)
	printPlannedDAG(stdout, task)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Next: `octo task run "+task.ID+"` (coming in PR3) — for now the DAG is on disk and resumable.")
	return 0
}

// printPlannedDAG renders the planned subtasks under the goal so the user
// can sanity-check the planner output before running. Format mirrors the
// task_manager renderer for visual consistency.
func printPlannedDAG(w io.Writer, t *taskgraph.Task) {
	fmt.Fprintf(w, "Goal: %s\n\n", t.Goal)
	fmt.Fprintln(w, "Plan:")
	for _, s := range t.Subtasks {
		fmt.Fprintf(w, "  #%-2d %s\n", s.ID, s.Description)
		if len(s.BlockedBy) > 0 {
			fmt.Fprintf(w, "      ↳ depends on: %s\n", joinInts(s.BlockedBy))
		}
	}
}

// oneLine collapses a multi-line goal to a single-line preview for the
// status line. Long goals are truncated so they don't wrap awkwardly.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		s = s[:77] + "…"
	}
	return s
}

// joinInts formats a []int as "1, 3, 4" for human display.
func joinInts(in []int) string {
	parts := make([]string, len(in))
	for i, n := range in {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, ", ")
}

// defaultMaxTokensForPlanner mirrors what `octo chat` defaults to when
// nothing is passed; the planner's actual cap is the much smaller
// planMaxTokens inside agent.PlanTask, but the agent struct still wants
// a sensible value.
const defaultMaxTokensForPlanner = 4096
