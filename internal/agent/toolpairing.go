package agent

// hasToolUse reports whether m contains any tool_use block.
func hasToolUse(m Message) bool {
	for _, b := range m.Blocks {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

// synthesizeInterruptedToolResults creates error tool_result blocks for each
// tool_use block in the given message. Used when a turn is interrupted and
// the tool_use has no matching tool_result (either because dispatchTools never
// ran, or because it failed to produce results before the interrupt).
//
// The error message "[Interrupted by user]" signals clearly to the LLM that
// the tool did not complete and should be retried if needed.
func synthesizeInterruptedToolResults(blocks []ContentBlock) []ContentBlock {
	var results []ContentBlock
	for _, b := range blocks {
		if b.Type == "tool_use" {
			results = append(results, NewToolResultBlock(b.ID, interruptNote, true))
		}
	}
	return results
}

// ensureToolPairing scans the history and fixes any orphaned tool_use blocks
// by synthesizing error tool_result blocks. This is a defensive check that
// runs before each send() call in runLoop to prevent Anthropic HTTP 400 errors
// ("tool_calls must be followed by tool messages").
//
// The function handles these cases:
//   - Orphaned assistant(tool_use) at the end of history (no following tool_result)
//   - Multiple consecutive tool_use messages without tool_results (rare edge case)
//
// Synthesized tool_results use is_error=true with "[Interrupted by user]" to
// signal clearly to the LLM that the tool did not complete.
func (a *Agent) ensureToolPairing() {
	msgs := a.History.Snapshot()
	if len(msgs) == 0 {
		return
	}

	// Scan for orphaned tool_use blocks. We need to track which tool_use IDs
	// have been answered by tool_result blocks in subsequent messages.
	//
	// Build a set of answered tool_use IDs from tool_result blocks.
	answered := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == RoleUser {
			for _, b := range m.Blocks {
				if b.Type == "tool_result" {
					answered[b.ToolUseID] = true
				}
			}
		}
	}

	// Find orphaned tool_use blocks (those without a matching tool_result).
	var orphans []ContentBlock
	for _, m := range msgs {
		if m.Role == RoleAssistant {
			for _, b := range m.Blocks {
				if b.Type == "tool_use" && !answered[b.ID] {
					orphans = append(orphans, b)
				}
			}
		}
	}

	if len(orphans) == 0 {
		return
	}

	// Synthesize error tool_results for all orphaned tool_use blocks.
	var results []ContentBlock
	for _, b := range orphans {
		results = append(results, NewToolResultBlock(b.ID, "[Tool execution was interrupted or failed to complete]", true))
	}
	a.History.Append(NewToolResultMessage(results))
}
