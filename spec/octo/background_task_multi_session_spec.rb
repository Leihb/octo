# frozen_string_literal: true

require "spec_helper"

# Multi-session isolation tests for the BackgroundTaskRegistry and the
# terminal tool's interaction with it. The registry is a process-level
# singleton — when several agent sessions run in the same process (which
# is exactly the server mode), each session must only see and be able to
# act on its own tasks.
RSpec.describe "BackgroundTaskRegistry multi-session isolation" do
  before { Octo::BackgroundTaskRegistry.reset! }
  after  { Octo::BackgroundTaskRegistry.reset! }

  describe ".list_running with agent_session_id filter" do
    it "returns only tasks belonging to the requested session" do
      id_a = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal",
        metadata: { command: "sleep 30", agent_session_id: "sess-A" }
      )
      id_b = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal",
        metadata: { command: "sleep 60", agent_session_id: "sess-B" }
      )

      running_a = Octo::BackgroundTaskRegistry.list_running(agent_session_id: "sess-A")
      running_b = Octo::BackgroundTaskRegistry.list_running(agent_session_id: "sess-B")

      expect(running_a.map { |t| t[:handle_id] }).to contain_exactly(id_a)
      expect(running_b.map { |t| t[:handle_id] }).to contain_exactly(id_b)
    end

    it "returns all running tasks when no agent_session_id is given" do
      Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-A" }
      )
      Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-B" }
      )

      expect(Octo::BackgroundTaskRegistry.list_running.size).to eq(2)
    end
  end

  describe "callback routing" do
    it "fires each session's callback only with its own task result" do
      received_a = Queue.new
      received_b = Queue.new

      id_a = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-A" }
      )
      id_b = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-B" }
      )

      Octo::BackgroundTaskRegistry.register_callback(handle_id: id_a, agent: nil) do |r|
        received_a << r
      end
      Octo::BackgroundTaskRegistry.register_callback(handle_id: id_b, agent: nil) do |r|
        received_b << r
      end

      Octo::BackgroundTaskRegistry.complete(id_b, { exit_code: 0, output: "B done", handle_id: id_b })
      Octo::BackgroundTaskRegistry.complete(id_a, { exit_code: 1, output: "A done", handle_id: id_a })

      result_a = received_a.pop
      result_b = received_b.pop

      expect(result_a[:output]).to eq("A done")
      expect(result_a[:exit_code]).to eq(1)
      expect(result_b[:output]).to eq("B done")
      expect(result_b[:exit_code]).to eq(0)
    end
  end

  describe ".prune_completed scope" do
    it "with agent_session_id, only prunes that session's completed tasks" do
      id_a = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-A" }
      )
      id_b = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-B" }
      )

      Octo::BackgroundTaskRegistry.complete(id_a, { exit_code: 0 })
      Octo::BackgroundTaskRegistry.complete(id_b, { exit_code: 0 })

      Octo::BackgroundTaskRegistry.instance_variable_get(:@tasks).each_value do |t|
        t[:completed_at] = Time.now - 7200
      end

      Octo::BackgroundTaskRegistry.prune_completed(max_age: 3600, agent_session_id: "sess-A")

      expect(Octo::BackgroundTaskRegistry.get(id_a)).to be_nil
      expect(Octo::BackgroundTaskRegistry.get(id_b)).not_to be_nil
    end

    it "without agent_session_id, prunes globally as before" do
      id_a = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-A" }
      )
      id_b = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-B" }
      )

      Octo::BackgroundTaskRegistry.complete(id_a, { exit_code: 0 })
      Octo::BackgroundTaskRegistry.complete(id_b, { exit_code: 0 })

      Octo::BackgroundTaskRegistry.instance_variable_get(:@tasks).each_value do |t|
        t[:completed_at] = Time.now - 7200
      end

      Octo::BackgroundTaskRegistry.prune_completed(max_age: 3600)

      expect(Octo::BackgroundTaskRegistry.get(id_a)).to be_nil
      expect(Octo::BackgroundTaskRegistry.get(id_b)).to be_nil
    end
  end

  describe "concurrent cancel does not leak across sessions" do
    it "cancelling one session's task leaves the other's callback intact" do
      received_a = Queue.new
      received_b = Queue.new

      id_a = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-A" }
      )
      id_b = Octo::BackgroundTaskRegistry.create_task(
        type: "terminal", metadata: { agent_session_id: "sess-B" }
      )

      Octo::BackgroundTaskRegistry.register_callback(handle_id: id_a, agent: nil) { |r| received_a << r }
      Octo::BackgroundTaskRegistry.register_callback(handle_id: id_b, agent: nil) { |r| received_b << r }

      Octo::BackgroundTaskRegistry.cancel(id_a)
      Octo::BackgroundTaskRegistry.complete(id_b, { exit_code: 0, output: "B finished" })

      ra = received_a.pop
      rb = received_b.pop

      expect(ra[:cancelled]).to be(true)
      expect(rb[:output]).to eq("B finished")
      expect(rb[:cancelled]).to be_nil
    end
  end
end

RSpec.describe "Terminal tool stamps agent_session_id on background tasks" do
  before { Octo::BackgroundTaskRegistry.reset! }
  after  { Octo::BackgroundTaskRegistry.reset! }

  def stub_background_path(tool, session_id:)
    fake_session = double("session", id: session_id, pid: 12345,
      writer: double(close: nil), reader: double(close: nil), log_io: double(close: nil),
      log_file: "/tmp/octo-terminals-test/#{session_id}.log")
    allow(tool).to receive(:spawn_dedicated_session).and_return(fake_session)
    allow(tool).to receive(:write_user_command)
    allow(tool).to receive(:wait_and_package).and_return(
      { session_id: session_id, state: "background", output: "", bytes_read: 0 }
    )
    allow(tool).to receive(:start_background_watcher)
    fake_session
  end

  it "passes agent_session_id from the tool to BackgroundTaskRegistry metadata" do
    tool = Octo::Tools::Terminal.new(agent_session_id: "sess-X")
    stub_background_path(tool, session_id: 42)

    captured = nil
    allow(Octo::BackgroundTaskRegistry).to receive(:create_task).and_wrap_original do |orig, **kwargs|
      captured = kwargs
      orig.call(**kwargs)
    end

    tool.execute(command: "sleep 30", async: true)

    expect(captured).not_to be_nil
    expect(captured[:metadata][:agent_session_id]).to eq("sess-X")
  end

  it "with two Terminal tools in the same process, list_running cleanly partitions" do
    tool_a = Octo::Tools::Terminal.new(agent_session_id: "sess-A")
    tool_b = Octo::Tools::Terminal.new(agent_session_id: "sess-B")

    stub_background_path(tool_a, session_id: 42)
    stub_background_path(tool_b, session_id: 43)

    tool_a.execute(command: "sleep 30", async: true)
    tool_b.execute(command: "sleep 60", async: true)

    a_running = Octo::BackgroundTaskRegistry.list_running(agent_session_id: "sess-A")
    b_running = Octo::BackgroundTaskRegistry.list_running(agent_session_id: "sess-B")

    expect(a_running.size).to eq(1)
    expect(a_running.first[:command]).to eq("sleep 30")
    expect(b_running.size).to eq(1)
    expect(b_running.first[:command]).to eq("sleep 60")
  end

  it "nil agent_session_id (CLI/standalone) still works and produces nil metadata" do
    tool = Octo::Tools::Terminal.new
    stub_background_path(tool, session_id: 44)

    captured = nil
    allow(Octo::BackgroundTaskRegistry).to receive(:create_task).and_wrap_original do |orig, **kwargs|
      captured = kwargs
      orig.call(**kwargs)
    end

    tool.execute(command: "sleep 5", async: true)

    expect(captured[:metadata]).to have_key(:agent_session_id)
    expect(captured[:metadata][:agent_session_id]).to be_nil
  end
end
