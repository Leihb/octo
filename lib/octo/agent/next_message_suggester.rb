# frozen_string_literal: true

module Octo
  class Agent
    # Background "ghost text" prediction of the user's next message.
    #
    # Fired after each main-agent task completes. Generates one short phrase
    # (the model's best guess at what the user will type next) and pushes it
    # to the UI via +show_next_message_suggestion+. The web UI renders it as
    # the input box's placeholder with Tab-to-accept; terminal / IM UIs are
    # no-ops by default.
    #
    # Design notes:
    #   - NOT a forked subagent. Subagents clone history, run a full
    #     think/act/observe loop, and trigger hooks — overkill for "generate
    #     one line." We make a single +Client#send_messages+ call directly.
    #   - Async (own thread) and fire-and-forget. +Agent#show_complete+ must
    #     never block on the suggestion call.
    #   - Uses the provider's lite model when available (Claude → Haiku,
    #     DeepSeek pro → flash, ...), falling back to the current primary
    #     when no lite mapping exists.
    #   - Reuses the main agent's system prompt + last few messages as the
    #     LLM input. When primary and lite share a provider, this lands on a
    #     warm prompt cache; otherwise the cost is small absolute (short
    #     prompt, ≤40 output tokens).
    #   - Silent on any failure. A failed suggestion call never disturbs the
    #     user's actual task result.
    module NextMessageSuggester
      # Max characters in a suggestion that we'll forward to the UI. Anything
      # longer is treated as a model misfire and dropped (it's supposed to be
      # a phrase, not a paragraph).
      MAX_SUGGESTION_CHARS = 80

      # How many recent message pairs to send as context. 4 is plenty for the
      # model to read the situation; more just inflates the cache footprint.
      RECENT_HISTORY_LIMIT = 8

      # Output budget — short phrases only.
      SUGGESTION_MAX_TOKENS = 40

      # Trigger predicate. Cheap; called on the agent thread.
      def next_message_suggestion_enabled?
        return false unless @config.respond_to?(:next_message_suggestion_enabled)
        return false unless @config.next_message_suggestion_enabled
        return false if @is_subagent
        true
      end

      # Spawn the suggestion call in a daemon thread. Returns immediately.
      def run_next_message_suggestion!
        return unless next_message_suggestion_enabled?
        return unless @ui

        # Snapshot the agent state we need on the worker thread so we don't
        # race with the next user turn mutating @history / @todos in place.
        history_snapshot = recent_history_for_suggestion
        return if history_snapshot.empty?

        todos_snapshot = (@todos || []).map { |t| t.is_a?(Hash) ? t.dup : t }
        ui = @ui

        Thread.new do
          text = generate_next_message_suggestion(history_snapshot, todos_snapshot)
          ui.show_next_message_suggestion(text) if text && !text.empty?
        rescue StandardError => e
          Octo::Logger.warn(
            "next_message_suggestion.failed",
            session_id: @session_id,
            error_class: e.class.name,
            error_message: e.message
          )
        end
      end

      private def generate_next_message_suggestion(history_snapshot, todos_snapshot)
        client, model_name = build_suggestion_client_and_model
        return nil unless client && model_name

        messages = build_suggestion_messages(history_snapshot, todos_snapshot)
        reply = client.send_messages(
          messages,
          model: model_name,
          max_tokens: SUGGESTION_MAX_TOKENS
        )

        sanitize_suggestion(reply)
      end

      # Pick the cheapest client/model that will give us a short reply.
      # Prefers the provider's lite model so a Claude-Sonnet primary spawns
      # a Haiku call; falls back to the primary client when no lite mapping
      # exists for the current provider.
      private def build_suggestion_client_and_model
        lite_cfg = @config.respond_to?(:lite_model_config_for_current) ? @config.lite_model_config_for_current : nil

        if lite_cfg && lite_cfg["model"] && lite_cfg["api_key"]
          client = Octo::Client.new(
            lite_cfg["api_key"],
            base_url: lite_cfg["base_url"],
            model: lite_cfg["model"],
            anthropic_format: lite_cfg["anthropic_format"] || false
          )
          [client, lite_cfg["model"]]
        else
          # Fall back to the primary client. Costs more per token, but the
          # prompt is short and the user explicitly hasn't configured a
          # cheaper lite — respect their config.
          [@client, @config.model_name]
        end
      rescue StandardError
        [nil, nil]
      end

      private def build_suggestion_messages(history_snapshot, todos_snapshot)
        [
          { role: "user", content: build_suggestion_prompt(history_snapshot, todos_snapshot) }
        ]
      end

      # We deliberately pass the prompt as a single user turn (not a
      # system + chat-history rerun) for three reasons:
      #   1. Some providers reject system-only prompts on the simple
      #      +send_messages+ path; staying within a plain user turn is
      #      universally portable.
      #   2. Cache hit isn't realistic anyway — the suggestion model is
      #      usually a different model than the main one (Haiku vs Sonnet),
      #      so the cache wouldn't apply.
      #   3. The prompt stays self-contained → trivial to log and replay.
      private def build_suggestion_prompt(history_snapshot, todos_snapshot)
        lines = []
        lines << "You are a UI helper. Predict the user's next message in this conversation."
        lines << ""
        lines << "Output ONE short phrase (≤15 chars Chinese, ≤30 chars English)."
        lines << "No quotes, no prefixes like 'User:'. Plain text only."
        lines << "If background work is running or todos are in progress, prefer phrases like '等 X 完成' / 'wait for X'."
        lines << "If you have no good guess, output the single token: NONE"
        lines << ""

        if todos_snapshot && !todos_snapshot.empty?
          lines << "Current todos:"
          todos_snapshot.first(6).each do |t|
            status  = t.is_a?(Hash) ? (t[:status] || t["status"]) : nil
            content = t.is_a?(Hash) ? (t[:content] || t["content"]) : t.to_s
            next unless content
            lines << "- [#{status || "?"}] #{content.to_s[0, 80]}"
          end
          lines << ""
        end

        lines << "Recent conversation (oldest first):"
        history_snapshot.each do |m|
          role    = m[:role] || m["role"]
          content = m[:content] || m["content"]
          next unless content
          rendered = render_content_for_suggestion(content)
          next if rendered.nil? || rendered.empty?
          lines << "#{role}: #{rendered[0, 400]}"
        end
        lines << ""
        lines << "Now output the predicted next user message:"

        lines.join("\n")
      end

      # Pull the last few non-transient, non-tool messages from history.
      # Excludes:
      #   - system messages (large, mostly noise for this prompt)
      #   - tool calls / results (verbose; the assistant message that
      #     surrounds them already conveys the gist)
      #   - system_injected user messages (file refs, compression hints)
      private def recent_history_for_suggestion
        all = @history.to_a
        usable = all.reject do |m|
          role = (m[:role] || m["role"]).to_s
          role == "system" ||
            role == "tool" ||
            m[:system_injected] || m["system_injected"]
        end
        usable.last(RECENT_HISTORY_LIMIT)
      end

      # Content in history can be a String OR an array of content blocks
      # (Anthropic-style: [{type:"text", text:"..."}, {type:"image_url", ...}]).
      # We only need text for the prompt.
      private def render_content_for_suggestion(content)
        case content
        when String
          content.strip
        when Array
          content.filter_map do |block|
            text = block.is_a?(Hash) ? (block[:text] || block["text"]) : nil
            text&.strip
          end.join(" ").strip
        else
          nil
        end
      end

      private def sanitize_suggestion(raw)
        return nil if raw.nil?
        text = raw.to_s.strip
        return nil if text.empty?
        # Take the first non-empty line — discard any chain-of-thought tail.
        first_line = text.lines.map(&:strip).find { |l| !l.empty? }
        return nil unless first_line
        # Strip wrapping quotes the model sometimes adds.
        first_line = first_line.gsub(/\A["'“”‘’`]+|["'“”‘’`]+\z/, "").strip
        return nil if first_line.empty?
        return nil if first_line.casecmp("NONE").zero?
        return nil if first_line.length > MAX_SUGGESTION_CHARS
        first_line
      end
    end
  end
end
