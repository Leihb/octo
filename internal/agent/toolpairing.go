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
// IMPORTANT: If the last message is a non-tool-result user message (e.g., from
// inbox drain), the synthetic tool_results are MERGED into that message rather
// than appended as a new message. This preserves the Anthropic API requirement
// that tool_use must be immediately followed by tool_result.
//
// Synthesized tool_results use is_error=true with "[Tool execution was interrupted]"
// to signal clearly to the LLM that the tool did not complete.
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

	// Check if the last message is a non-tool-result user message (e.g., from
	// inbox drain). If so, we need to MERGE the synthetic tool_results into
	// that message to preserve the tool_use/tool_result pairing requirement.
	lastMsg := msgs[len(msgs)-1]
	if lastMsg.Role == RoleUser && !hasToolResult(lastMsg) {
		// Merge: replace the last user message with one that contains both
		// the original content AND the synthetic tool_results.
		// The tool_results must come FIRST to satisfy the API requirement.
		mergedBlocks := make([]ContentBlock, 0, len(results)+1)
		mergedBlocks = append(mergedBlocks, results...)

		// Add the original content as a text block
		if lastMsg.Content != "" {
			mergedBlocks = append(mergedBlocks, NewTextBlock(lastMsg.Content))
		}
		// Add any existing blocks from the last message
		mergedBlocks = append(mergedBlocks, lastMsg.Blocks...)

		// Replace the last message in history
		a.History.replaceLast(Message{
			Role:   RoleUser,
			Blocks: mergedBlocks,
		})
	} else {
		// No merge needed - just append as a new user message
		a.History.Append(NewToolResultMessage(results))
	}
}
