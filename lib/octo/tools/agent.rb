# frozen_string_literal: true

module Octo
  module Tools
    # Spawns a forked sub-agent to handle an isolated sub-task.
    #
    # This is the LLM-facing entry to Octo's existing fork_subagent
    # infrastructure: the parent agent describes a task, the sub-agent runs
    # autonomously against its own copy of history (with optional tool
    # restrictions and a different model), and returns a summary string.
    #
    # Sub-agents inherit:
    #   - All tool definitions (for prompt-cache reuse)
    #   - A deep clone of the parent's history
    #   - The same working_dir, agent profile, and source channel
    #
    # Sub-agents do NOT inherit the ability to call this very tool — the
    # default forbidden_tools includes `agent` itself, blocking infinite
    # recursion. Callers can extend the denylist but cannot disable the
    # self-block.
    class Agent < Base
      self.tool_name = "agent"
      self.tool_description = <<~DESC.strip
        Launch a new sub-agent to handle complex, multi-step tasks in isolation.

        Use this when you need to:
        - Run independent research that would clutter the main conversation
        - Parallelize work that can be split into self-contained sub-tasks
        - Verify code changes with a fresh perspective (no context contamination)
        - Constrain a sub-task to a smaller toolset (e.g. read-only exploration)

        Brief the sub-agent like a colleague who just walked in: include the goal,
        what is already known, what's in scope, and what success looks like. The
        sub-agent cannot ask follow-up questions of the parent — its result
        comes back as a single tool result string.

        Defaults to the lite model for cost; pass `model: "default"` for primary.
        Recursion is blocked: a sub-agent cannot itself call `agent`.
      DESC
      self.tool_category = "subagent"
      self.tool_parameters = {
        type: "object",
        properties: {
          description: {
            type: "string",
            description: "Short 3-5 word label shown to the user while the sub-agent runs (e.g. 'Audit auth flow')"
          },
          prompt: {
            type: "string",
            description: "Self-contained task brief for the sub-agent: goal, context, constraints, what success looks like"
          },
          subagent_type: {
            type: "string",
            description: "Optional preset name from default_agents/ (reserved for P1-1; ignored today)"
          },
          tools: {
            type: "array",
            items: { type: "string" },
            description: "Optional allowlist of tool names; all other tools become forbidden in the sub-agent (the `agent` tool itself is always forbidden)"
          },
          forbidden_tools: {
            type: "array",
            items: { type: "string" },
            description: "Optional denylist merged with the default `agent` self-block"
          },
          model: {
            type: "string",
            description: "Model role: `lite` (default, cheap) | `default` (primary) | a configured model name"
          }
        },
        required: %w[description prompt]
      }

      # Execute the sub-agent task.
      #
      # @param description [String] short label for UI
      # @param prompt [String] task brief delivered as the sub-agent's user turn
      # @param subagent_type [String, nil] preset name (P1-1 hook; ignored today)
      # @param tools [Array<String>, nil] allowlist of tool names
      # @param forbidden_tools [Array<String>, nil] explicit denylist
      # @param model [String] model role / name (default: "lite")
      # @param agent [Octo::Agent] injected by the dispatcher
      # @param working_dir [String, nil] injected by the dispatcher (unused; here for parity)
      # @return [String, Hash] sub-agent summary on success, error hash on misuse
      def execute(description:, prompt:, subagent_type: nil, tools: nil,
                  forbidden_tools: nil, model: "lite", agent: nil, working_dir: nil)
        _ = working_dir  # accepted but unused — sub-agent inherits parent's working_dir
        _ = subagent_type  # reserved for P1-1 preset loading
        return { error: "Agent context is required" } unless agent
        return { error: "description is required" } if description.to_s.strip.empty?
        return { error: "prompt is required" } if prompt.to_s.strip.empty?

        effective_forbidden = resolve_forbidden(agent, tools: tools, forbidden_tools: forbidden_tools)

        subagent = agent.fork_subagent(
          model: model,
          forbidden_tools: effective_forbidden,
          system_prompt_suffix: build_subagent_brief(description)
        )

        agent.ui&.show_info(
          "Sub-agent start: #{description} [#{subagent.current_model_info&.dig(:model)}]"
        )

        begin
          subagent.run(prompt)
        rescue Octo::AgentInterrupted
          # Bubble up so the parent loop exits cleanly too.
          raise
        end

        summary = agent.send(:generate_subagent_summary, subagent)

        # Roll the sub-agent's token + cost into the parent's session totals
        # so /cost and max_cost_usd see the full bill for the user request.
        absorb_session_usage(agent, subagent)

        agent.ui&.show_info("Sub-agent done: #{description} (#{subagent.iterations} iterations)")

        summary
      end

      # @param args [Hash]
      # @return [String]
      def format_call(args)
        desc = args[:description] || args["description"]
        "Agent(#{desc})"
      end

      # @param result [Object]
      # @return [String]
      def format_result(result)
        if result.is_a?(Hash) && result[:error]
          "Error: #{result[:error]}"
        elsif result.is_a?(String)
          first_line = result.lines.first.to_s.strip
          first_line.length > 120 ? "#{first_line[0..117]}..." : first_line
        else
          "Sub-agent finished"
        end
      end

      private def resolve_forbidden(agent, tools:, forbidden_tools:)
        denylist = Array(forbidden_tools).map(&:to_s)

        if tools && !Array(tools).empty?
          # Allowlist takes precedence: forbid everything not on the list.
          # Plain Array#include? avoids a Set require on Ruby 2.6 (CI matrix).
          allow = Array(tools).map(&:to_s)
          all_names = agent.instance_variable_get(:@tool_registry).all.map(&:name)
          all_names.each do |name|
            denylist << name unless allow.include?(name)
          end
        end

        # Always block self-recursion.
        denylist << self.class.tool_name unless denylist.include?(self.class.tool_name)
        denylist.uniq
      end

      private def build_subagent_brief(description)
        <<~BRIEF.strip
          You have been spawned by the parent agent to handle this sub-task: #{description}

          Work autonomously. You cannot ask follow-up questions of the parent — your
          response must be self-contained. When you finish (i.e. stop calling tools
          and produce a final response), your output will be summarized and handed
          back to the parent as a tool result.
        BRIEF
      end

      private def absorb_session_usage(parent, subagent)
        return unless parent && subagent
        parent_totals = parent.session_token_totals
        sub_totals = subagent.session_token_totals
        parent_totals[:prompt_tokens] += sub_totals[:prompt_tokens]
        parent_totals[:completion_tokens] += sub_totals[:completion_tokens]
        parent_totals[:cache_creation_input_tokens] += sub_totals[:cache_creation_input_tokens]
        parent_totals[:cache_read_input_tokens] += sub_totals[:cache_read_input_tokens]
        parent.instance_variable_set(
          :@session_cost_usd,
          parent.session_cost_usd + subagent.session_cost_usd
        )
      end
    end
  end
end
