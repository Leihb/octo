# frozen_string_literal: true

require "spec_helper"

RSpec.describe Octo::BackgroundTaskRegistry do
  before do
    described_class.reset!
  end

  after do
    described_class.reset!
  end

  describe ".create_task" do
    it "returns a UUID and stores running state" do
      id = described_class.create_task(type: "terminal", metadata: { command: "sleep 5" })
      expect(id).to match(/\A[a-z0-9]{9}\z/)
      task = described_class.get(id)
      expect(task[:status]).to eq("running")
      expect(task[:metadata][:command]).to eq("sleep 5")
    end
  end

  describe ".complete" do
    it "fires the registered callback on a fresh thread with the result" do
      id = described_class.create_task(type: "terminal", metadata: {})
      received = Queue.new

      described_class.register_callback(handle_id: id, agent: nil) do |result|
        received << result
      end

      described_class.complete(id, { exit_code: 0, output: "ok" })

      result = received.pop
      expect(result[:exit_code]).to eq(0)
      expect(result[:output]).to eq("ok")
      expect(described_class.get(id)[:status]).to eq("completed")
    end

    it "does not fire callback twice if complete called twice" do
      id = described_class.create_task(type: "terminal", metadata: {})
      fired = Queue.new

      described_class.register_callback(handle_id: id, agent: nil) do |r|
        fired << r
      end

      described_class.complete(id, { exit_code: 0 })
      described_class.complete(id, { exit_code: 0 })

      sleep 0.1
      # First complete fires, second finds no callback (deleted on first)
      expect(fired.size).to eq(1)
    end

    it "is a no-op for an unknown handle_id" do
      expect {
        described_class.complete("nonexistent-uuid", { exit_code: 0 })
      }.not_to raise_error
    end

    it "does not overwrite a cancelled task" do
      id = described_class.create_task(type: "terminal", metadata: {})
      described_class.cancel(id)
      described_class.complete(id, { exit_code: 0, output: "should be ignored" })

      task = described_class.get(id)
      expect(task[:status]).to eq("cancelled")
    end

    it "enriches the callback result with elapsed_seconds and started_at" do
      id = described_class.create_task(type: "terminal", metadata: {})
      # Backdate created_at so elapsed math has something to measure.
      described_class.instance_variable_get(:@tasks)[id][:created_at] = Time.now - 3
      received = Queue.new
      described_class.register_callback(handle_id: id, agent: nil) { |r| received << r }

      described_class.complete(id, { exit_code: 0, output: "ok" })
      result = received.pop

      expect(result[:elapsed_seconds]).to be_between(3, 4)
      expect(result[:started_at]).to be_a(Time)
    end
  end

  describe ".register_callback (race-safe)" do
    it "fires immediately if the task already completed before registration" do
      id = described_class.create_task(type: "terminal", metadata: {})

      # Simulate the race: task completes BEFORE the agent registers
      # its callback. Without the fix, this notification would be lost.
      described_class.complete(id, { exit_code: 0, output: "fast" })

      received = Queue.new
      registered = described_class.register_callback(handle_id: id, agent: nil) do |r|
        received << r
      end

      expect(registered).to be true
      result = received.pop
      expect(result[:exit_code]).to eq(0)
      expect(result[:output]).to eq("fast")
    end

    it "fires immediately with a cancellation result if task already cancelled" do
      id = described_class.create_task(type: "terminal", metadata: {})
      described_class.cancel(id)

      received = Queue.new
      described_class.register_callback(handle_id: id, agent: nil) do |r|
        received << r
      end

      result = received.pop
      expect(result[:cancelled]).to be true
      expect(result[:state]).to eq("cancelled")
    end

    it "returns false for an unknown handle_id" do
      result = described_class.register_callback(handle_id: "nonexistent", agent: nil) { }
      expect(result).to be false
    end
  end

  describe ".cancel" do
    it "invokes the on_cancel hook and the registered callback" do
      cancel_hook_called = false
      id = described_class.create_task(
        type: "terminal",
        metadata: {},
        on_cancel: ->(_t) { cancel_hook_called = true }
      )

      received = Queue.new
      described_class.register_callback(handle_id: id, agent: nil) do |r|
        received << r
      end

      expect(described_class.cancel(id)).to be true
      result = received.pop
      expect(result[:cancelled]).to be true
      expect(cancel_hook_called).to be true
    end

    it "returns false when cancelling an already-completed task" do
      id = described_class.create_task(type: "terminal", metadata: {})
      described_class.complete(id, { exit_code: 0 })
      expect(described_class.cancel(id)).to be false
    end

    it "swallows on_cancel exceptions and still marks task cancelled" do
      id = described_class.create_task(
        type: "terminal",
        metadata: {},
        on_cancel: ->(_t) { raise "boom" }
      )
      allow(Octo::Logger).to receive(:error)

      expect(described_class.cancel(id)).to be true
      expect(described_class.get(id)[:status]).to eq("cancelled")
    end
  end

  describe ".list_running" do
    it "filters by agent_session_id when provided" do
      a = described_class.create_task(type: "terminal", metadata: { agent_session_id: "s1", command: "a" })
      b = described_class.create_task(type: "terminal", metadata: { agent_session_id: "s2", command: "b" })
      described_class.create_task(type: "terminal", metadata: { agent_session_id: "s1", command: "c" })

      s1_tasks = described_class.list_running(agent_session_id: "s1")
      expect(s1_tasks.map { |t| t[:handle_id] }).to contain_exactly(a, described_class.list_running.find { |t| t[:command] == "c" }[:handle_id])
      expect(s1_tasks.map { |t| t[:command] }).to contain_exactly("a", "c")

      s2_tasks = described_class.list_running(agent_session_id: "s2")
      expect(s2_tasks.map { |t| t[:handle_id] }).to eq([b])
    end

    it "excludes completed/cancelled tasks" do
      running = described_class.create_task(type: "terminal", metadata: {})
      done    = described_class.create_task(type: "terminal", metadata: {})
      described_class.complete(done, { exit_code: 0 })

      ids = described_class.list_running.map { |t| t[:handle_id] }
      expect(ids).to include(running)
      expect(ids).not_to include(done)
    end
  end

  describe ".prune_completed" do
    it "removes completed tasks older than max_age" do
      old = described_class.create_task(type: "terminal", metadata: {})
      described_class.complete(old, { exit_code: 0 })
      # Manually backdate completed_at to simulate an old task
      described_class.instance_variable_get(:@tasks)[old][:completed_at] = Time.now - 7_200

      fresh = described_class.create_task(type: "terminal", metadata: {})
      described_class.complete(fresh, { exit_code: 0 })

      described_class.prune_completed(max_age: 3_600)

      expect(described_class.get(old)).to be_nil
      expect(described_class.get(fresh)).not_to be_nil
    end

    it "never removes running tasks" do
      running = described_class.create_task(type: "terminal", metadata: {})
      described_class.prune_completed(max_age: 0)
      expect(described_class.get(running)).not_to be_nil
    end
  end
end
