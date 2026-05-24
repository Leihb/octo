# frozen_string_literal: true

require "securerandom"

module Octo
  class BackgroundTaskRegistry
    @tasks          = {}
    @callbacks      = {}
    @mutex          = Mutex.new
    @sweep_running  = false

    HANDLE_ALPHABET  = (('a'..'z').to_a + ('0'..'9').to_a).freeze
    HANDLE_LENGTH    = 9

    TTL_UNWATCHED    = 600
    SWEEP_INTERVAL   = 30

    class << self
      def create_task(type:, metadata: {}, on_cancel: nil)
        @mutex.synchronize do
          handle_id = generate_unique_handle_id
          @tasks[handle_id] = {
            id: handle_id,
            type: type,
            status: "running",
            metadata: metadata,
            result: nil,
            created_at: Time.now,
            completed_at: nil,
            on_cancel: on_cancel,
            last_activity_at: metadata[:watched] ? nil : Time.now
          }

          ensure_sweep_thread unless metadata[:watched]

          handle_id
        end
      end

      def register_callback(handle_id:, agent:, &block)
        fire_immediately = nil
        captured_task    = nil

        @mutex.synchronize do
          task = @tasks[handle_id]
          return false unless task

          if task[:status] == "completed" || task[:status] == "cancelled"
            fire_immediately = task[:result] || {
              cancelled: task[:status] == "cancelled",
              output: task[:status] == "cancelled" ? "Task was cancelled by user." : "",
              exit_code: nil,
              state: task[:status]
            }
            captured_task = task
          else
            @callbacks[handle_id] = {
              agent: agent,
              callback: block,
              registered_at: Time.now
            }
          end
        end

        fire_immediately = enrich_with_timing(fire_immediately, captured_task) if fire_immediately

        if fire_immediately
          Thread.new do
            Thread.current.name = "bg-task-notify-late-#{handle_id[0, 8]}"
            begin
              block.call(fire_immediately)
            rescue => e
              Octo::Logger.warn("background_task_callback_retry",
                handle_id: handle_id,
                agent_session: agent&.session_id,
                error: e
              )
              begin
                sleep 0.5
                block.call(fire_immediately)
              rescue => e2
                Octo::Logger.error("background_task_callback_error",
                  handle_id: handle_id,
                  agent_session: agent&.session_id,
                  error: e2
                )
              end
            end
          end
        end

        true
      end

      def cancel(handle_id)
        task = nil
        handler = nil

        @mutex.synchronize do
          task = @tasks[handle_id]
          return false unless task
          return false if task[:status] == "completed" || task[:status] == "cancelled"

          task[:status] = "cancelled"
          task[:completed_at] = Time.now
          handler = @callbacks.delete(handle_id)
        end

        begin
          task[:on_cancel]&.call(task)
        rescue => e
          Octo::Logger.error("background_task_cancel_hook_error",
            handle_id: handle_id,
            error: e
          )
        end

        if handler
          enriched = enrich_with_timing({
            cancelled: true,
            output: "Task was cancelled by user.",
            exit_code: nil,
            state: "cancelled"
          }, task)
          Thread.new do
            Thread.current.name = "bg-task-cancel-#{handle_id[0, 8]}"
            begin
              handler[:callback].call(enriched)
            rescue => e
              Octo::Logger.warn("background_task_callback_retry",
                handle_id: handle_id,
                agent_session: handler[:agent]&.session_id,
                error: e
              )
              begin
                sleep 0.5
                handler[:callback].call(enriched)
              rescue => e2
                Octo::Logger.error("background_task_callback_error",
                  handle_id: handle_id,
                  agent_session: handler[:agent]&.session_id,
                  error: e2
                )
              end
            end
          end
        end

        true
      end

      def complete(handle_id, result)
        task = nil
        handler = nil

        @mutex.synchronize do
          task = @tasks[handle_id]
          return unless task
          return if task[:status] == "cancelled"

          task[:status] = "completed"
          task[:result] = result
          task[:completed_at] = Time.now

          handler = @callbacks.delete(handle_id)
        end

        return unless handler

        enriched = enrich_with_timing(result, task)

        Thread.new do
          Thread.current.name = "bg-task-notify-#{handle_id[0, 8]}"
          begin
            handler[:callback].call(enriched)
          rescue => e
            Octo::Logger.warn("background_task_callback_retry",
              handle_id: handle_id,
              agent_session: handler[:agent]&.session_id,
              error: e
            )
            begin
              sleep 0.5
              handler[:callback].call(enriched)
            rescue => e2
              Octo::Logger.error("background_task_callback_error",
                handle_id: handle_id,
                agent_session: handler[:agent]&.session_id,
                error: e2
              )
            end
          end
        end
      end

      def list_running(agent_session_id: nil)
        @mutex.synchronize do
          tasks = @tasks.values.select { |t| t[:status] == "running" }
          tasks = tasks.select { |t| t[:metadata][:agent_session_id] == agent_session_id } if agent_session_id
          tasks.map do |t|
            {
              handle_id: t[:id],
              type: t[:type],
              command: t[:metadata][:command],
              started_at: t[:created_at]
            }
          end
        end
      end

      def get(handle_id)
        @mutex.synchronize { @tasks[handle_id]&.dup }
      end

      def record_activity(handle_id)
        @mutex.synchronize do
          task = @tasks[handle_id]
          task[:last_activity_at] = Time.now if task
        end
      end

      def forget(handle_id)
        @mutex.synchronize do
          @tasks.delete(handle_id)
          @callbacks.delete(handle_id)
        end
      end

      def prune_completed(max_age: 3600, agent_session_id: nil)
        cutoff = Time.now - max_age
        @mutex.synchronize do
          @tasks.delete_if do |_id, task|
            next false unless task[:status] == "completed"
            next false unless task[:completed_at] && task[:completed_at] < cutoff
            next false if agent_session_id && task[:metadata][:agent_session_id] != agent_session_id
            true
          end
        end
      end

      def reset!
        @mutex.synchronize do
          @tasks.clear
          @callbacks.clear
          @sweep_running = false
        end
      end

      private def ensure_sweep_thread
        return if @sweep_running
        @sweep_running = true
        Thread.new do
          Thread.current.name = "bg-task-sweep"
          sweep_loop
        end
      end

      private def sweep_loop
        while @sweep_running
          sleep SWEEP_INTERVAL
          break unless @sweep_running

          to_cancel = []
          @mutex.synchronize do
            now = Time.now
            @tasks.each do |handle_id, task|
              next unless task[:status] == "running"
              next if task[:metadata]&.[](:watched)

              last_activity = task[:last_activity_at] || task[:created_at]
              next unless last_activity
              next if now - last_activity < TTL_UNWATCHED

              to_cancel << handle_id
            end
          end

          to_cancel.each do |handle_id|
            begin
              cancel(handle_id)
              Octo::Logger.info("bg_task_ttl_cleanup",
                handle_id: handle_id,
                reason: "unwatched handle exceeded #{TTL_UNWATCHED}s TTL"
              )
            rescue => e
              Octo::Logger.error("bg_task_sweep_error",
                handle_id: handle_id,
                error: e
              )
            end
          end
        end
      end

      private def generate_unique_handle_id
        loop do
          id = HANDLE_LENGTH.times.map { HANDLE_ALPHABET[SecureRandom.random_number(HANDLE_ALPHABET.size)] }.join
          return id unless @tasks.key?(id)
        end
      end

      private def enrich_with_timing(result, task)
        return result unless task && task[:created_at] && task[:completed_at]
        result.merge(
          started_at:      task[:created_at],
          elapsed_seconds: (task[:completed_at] - task[:created_at]).round
        )
      end
    end
  end
end
