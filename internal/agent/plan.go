package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// planMaxTokens caps the planner side-call output. A DAG with brief
// descriptions per node fits comfortably in a few KB; we set a generous
// ceiling so unusually large plans still go through without truncation.
const planMaxTokens = 4096

// planSystem is the planner's standalone system prompt. The model gets the
// user's goal as a user message and emits a JSON object describing a DAG of
// subtasks the M11 scheduler will execute via M10 sub-agents.
//
// The prompt borrows the same shape as extract.go (no-op gate, schema
// up-front, anti-patterns, output discipline) so a planner side-call has
// the same predictable behaviour as the memory extract pass.
const planSystem = `You decompose a user's autonomous-work goal into a small DAG of focused
subtasks. Each subtask will be handed to an isolated sub-agent that has the
same tools you have, runs in its own context (no visibility into this
conversation), and reports a single text reply when done.

Output ONE JSON object with one top-level field:

  {
    "subtasks": [
      { "description": "<what the sub-agent should do>", "blocked_by": [<ids>] },
      ...
    ]
  }

The runtime assigns sequential 1-based ids (1, 2, 3, …) to the list, so
"blocked_by" entries reference earlier subtasks BY INDEX (1-based). The
first subtask cannot have blocked_by. blocked_by may be omitted when there
are no dependencies.

================ WHAT A GOOD PLAN LOOKS LIKE ================
- Between 1 and ~12 subtasks. If the goal is trivial, ONE subtask is fine
  — don't manufacture work to look thorough.
- Each subtask is self-contained: a sub-agent reading just that description
  (and the upstream subtasks' completed results) should know exactly what to
  do without asking follow-up questions.
- Each subtask is small enough to complete in roughly 5-15 sub-agent turns.
  Massive "do the whole feature" subtasks defeat the point of the DAG.
- Dependencies model what genuinely BLOCKS what. If two subtasks could run
  in parallel, leave them independent (blocked_by absent) — the scheduler
  fans them out.

================ ANTI-PATTERNS ================
- Don't make every subtask depend on the previous one out of habit. That
  serializes work that could parallelize.
- Don't put "review", "verify", "test" as separate trailing subtasks unless
  the user asked for that workflow. Most subtasks should verify themselves.
- Don't restate the goal as the first subtask — start with the first
  concrete step.
- Don't include sub-tasks for setup that the harness handles (git, branch,
  PR, deploy). Stay inside the engineering work.
- Don't try to be exhaustive — a sub-agent will figure out the details
  inside its own context. You're sketching, not specifying.

================ OUTPUT ================
One JSON object. No prose, no code fences. If the goal is unclear or
impossible to plan from what you were given, return
{"subtasks": [{"description": "<one-line summary of the ambiguity>"}]} and
let the user provide more context after seeing the plan.`

// PlanResult is what the planner side-call returns.
type PlanResult struct {
	// Subtasks is the DAG the planner produced, in order. The runtime
	// stamps 1-based IDs (so Subtasks[0] becomes id 1) before persisting.
	Subtasks []PlannedSubtask
}

// PlannedSubtask is one node in the planner's output. IDs are assigned by
// the runtime — the planner only emits descriptions + dependencies.
type PlannedSubtask struct {
	Description string `json:"description"`
	BlockedBy   []int  `json:"blocked_by,omitempty"`
}

// maxProjectContextChars caps the project context injected into the planner
// user message. The planner is a side-call with a tight token budget; we
// truncate aggressively rather than risk pushing the context over the limit.
const maxProjectContextChars = 4000

// projectContextFile is the per-repo conventions file the planner reads to
// understand what kind of project it is planning for. Kept as a constant so
// it matches prompt.ProjectContextFile without importing prompt.
const projectContextFile = ".octorules"

// PlanTask runs the planner side-call over goal and returns the resulting
// subtask DAG. It does not write anything to disk — the caller persists
// via internal/taskgraph.
//
// A zero PlanResult means the planner emitted nothing usable (no JSON
// object found, or an empty subtasks array). Callers should treat that as
// a "couldn't plan" signal and surface it to the user.
func (a *Agent) PlanTask(ctx context.Context, goal string) (PlanResult, error) {
	if a.Sender == nil {
		return PlanResult{}, fmt.Errorf("agent: no Sender configured")
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return PlanResult{}, fmt.Errorf("agent: goal is required")
	}

	ctxText := readProjectContext(a.CWD)
	var userMsg string
	if ctxText != "" {
		userMsg = "Project context:\n" + ctxText + "\n\nGoal:\n\n" + goal + "\n\nPlan the subtask DAG per your instructions. Output only the JSON object."
	} else {
		userMsg = "Goal:\n\n" + goal + "\n\nPlan the subtask DAG per your instructions. Output only the JSON object."
	}

	req := []Message{NewUserMessage(userMsg)}
	reply, err := a.Sender.SendMessages(ctx, a.Model, planSystem, req, planMaxTokens)
	if err != nil {
		return PlanResult{}, err
	}
	a.sessionInputTokens += reply.InputTokens
	a.sessionOutputTokens += reply.OutputTokens
	return parsePlan(reply.Content)
}

// readProjectContext returns the trimmed contents of .octorules in cwd, or
// the current working directory if cwd is empty. The result is truncated to
// maxProjectContextChars to stay within the planner's token budget. Returns
// "" when the file is absent, unreadable, or empty.
func readProjectContext(cwd string) string {
	dir := cwd
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, projectContextFile))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) > maxProjectContextChars {
		s = s[:maxProjectContextChars] + "\n... [truncated]"
	}
	return s
}

// parsePlan extracts the JSON object from the planner's reply (tolerating
// a code fence or surrounding prose) and validates the rough structure.
// Doesn't check DAG invariants (that's taskgraph.validateSubtasks); just
// surfaces obviously-broken planner output before we reach the persistence
// layer.
func parsePlan(s string) (PlanResult, error) {
	s = strings.TrimSpace(stripCodeFence(s))
	if s == "" {
		return PlanResult{}, nil
	}
	first, _ := firstJSONChar(s)
	if first != '{' {
		// We could fall back to array-shape for forgiveness, but the
		// planner schema is well-defined enough that anything else is a
		// real planner error — surface it instead of papering over.
		return PlanResult{}, fmt.Errorf("agent: planner output does not start with a JSON object")
	}

	obj := sliceBetween(s, '{', '}')
	if obj == "" {
		return PlanResult{}, fmt.Errorf("agent: planner output has no closing brace")
	}

	var raw struct {
		Subtasks []PlannedSubtask `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(obj), &raw); err != nil {
		return PlanResult{}, fmt.Errorf("agent: parse planner output: %w", err)
	}

	out := make([]PlannedSubtask, 0, len(raw.Subtasks))
	for _, st := range raw.Subtasks {
		desc := strings.TrimSpace(st.Description)
		if desc == "" {
			continue // ignore empty entries silently — they're filler
		}
		out = append(out, PlannedSubtask{Description: desc, BlockedBy: st.BlockedBy})
	}
	if len(out) == 0 {
		return PlanResult{}, nil
	}
	return PlanResult{Subtasks: out}, nil
}
