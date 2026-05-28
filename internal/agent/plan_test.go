package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParsePlan_Object(t *testing.T) {
	in := `{
	  "subtasks": [
	    {"description": "Investigate the auth module"},
	    {"description": "Draft the migration script", "blocked_by": [1]},
	    {"description": "Run the test suite", "blocked_by": [2]}
	  ]
	}`
	r, err := parsePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Subtasks) != 3 {
		t.Fatalf("Subtasks len = %d, want 3", len(r.Subtasks))
	}
	if r.Subtasks[0].Description != "Investigate the auth module" {
		t.Errorf("first description = %q", r.Subtasks[0].Description)
	}
	if len(r.Subtasks[1].BlockedBy) != 1 || r.Subtasks[1].BlockedBy[0] != 1 {
		t.Errorf("blocked_by = %v", r.Subtasks[1].BlockedBy)
	}
}

func TestParsePlan_FencedAndPreamble(t *testing.T) {
	fenced := "```json\n{\"subtasks\":[{\"description\":\"do X\"}]}\n```"
	r, _ := parsePlan(fenced)
	if len(r.Subtasks) != 1 {
		t.Errorf("fenced object should parse: %+v", r)
	}
	withProse := "Here you go:\n{\"subtasks\":[{\"description\":\"do X\"}]}"
	r, _ = parsePlan(withProse)
	if len(r.Subtasks) != 1 {
		t.Errorf("preamble should not block parsing: %+v", r)
	}
}

func TestParsePlan_EmptySubtasks(t *testing.T) {
	r, err := parsePlan(`{"subtasks": []}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Subtasks) != 0 {
		t.Errorf("empty subtasks should yield empty PlanResult, got %+v", r)
	}
}

func TestParsePlan_DropsEmptyDescriptions(t *testing.T) {
	// Defensive: a planner that pads its output with empty entries shouldn't
	// poison the DAG. Filter them silently rather than error.
	in := `{"subtasks":[{"description":"real one"},{"description":""},{"description":"   "}]}`
	r, _ := parsePlan(in)
	if len(r.Subtasks) != 1 {
		t.Errorf("empty descriptions should be dropped, got %+v", r.Subtasks)
	}
}

func TestParsePlan_NonObjectErrors(t *testing.T) {
	// Bare array — the planner schema is well-defined; surface this as an
	// error rather than guessing.
	if _, err := parsePlan(`[{"description":"x"}]`); err == nil {
		t.Error("array shape should error (planner contract is an object)")
	}
}

func TestParsePlan_MalformedJSONErrors(t *testing.T) {
	if _, err := parsePlan(`{"subtasks":[{"description":}`); err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestParsePlan_EmptyInputReturnsZero(t *testing.T) {
	r, err := parsePlan("")
	if err != nil {
		t.Errorf("empty input shouldn't error, got %v", err)
	}
	if len(r.Subtasks) != 0 {
		t.Errorf("empty input → zero PlanResult, got %+v", r)
	}
}

func TestPlanTask_Forwarding(t *testing.T) {
	s := &stubExtractSender{reply: `{"subtasks":[{"description":"step one"},{"description":"step two","blocked_by":[1]}]}`}
	a := New(s, "test-model")

	r, err := a.PlanTask(context.Background(), "Migrate the auth middleware")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Subtasks) != 2 {
		t.Fatalf("Subtasks = %d, want 2", len(r.Subtasks))
	}
	if r.Subtasks[1].Description != "step two" {
		t.Errorf("second description = %q", r.Subtasks[1].Description)
	}
	if !strings.Contains(s.lastSystem, "decompose") {
		t.Errorf("PlanTask should use the planner system prompt, got %q", s.lastSystem)
	}
	if len(s.lastMessages) == 0 || !strings.Contains(s.lastMessages[0].Content, "Migrate the auth middleware") {
		t.Errorf("goal should be in the user message: %+v", s.lastMessages)
	}
}

func TestPlanTask_EmptyGoalErrors(t *testing.T) {
	a := New(&stubExtractSender{}, "test-model")
	if _, err := a.PlanTask(context.Background(), ""); err == nil {
		t.Error("empty goal should error")
	}
	if _, err := a.PlanTask(context.Background(), "   "); err == nil {
		t.Error("whitespace-only goal should error")
	}
}

func TestPlanTask_NoSender(t *testing.T) {
	a := New(nil, "test-model")
	if _, err := a.PlanTask(context.Background(), "goal"); err == nil {
		t.Error("missing Sender should error")
	}
}

func TestPlanTask_SenderErrorPropagates(t *testing.T) {
	s := &stubExtractSender{err: errors.New("provider down")}
	a := New(s, "test-model")
	if _, err := a.PlanTask(context.Background(), "goal"); err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Errorf("expected propagated sender error, got %v", err)
	}
}

func TestPlanTask_TokenUsageAccrued(t *testing.T) {
	s := &stubExtractSender{reply: `{"subtasks":[{"description":"x"}]}`}
	a := New(s, "test-model")
	_, _ = a.PlanTask(context.Background(), "goal")
	in, out := a.SessionTokens()
	if in == 0 || out == 0 {
		t.Errorf("PlanTask should accrue token usage, got (%d,%d)", in, out)
	}
}
