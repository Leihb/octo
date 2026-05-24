# frozen_string_literal: true

require "spec_helper"

RSpec.describe Octo::Server::WebUIController do
  let(:session_id) { "sess-abc-123" }
  let(:events)     { [] }
  let(:broadcaster) do
    ->(sid, ev) { events << { sid: sid, ev: ev } }
  end
  let(:controller) { described_class.new(session_id, broadcaster) }

  describe "#update_background_tasks" do
    it "emits background_tasks_update with running count and safe tasks" do
      controller.update_background_tasks(
        running: 2,
        tasks: [
          { handle_id: "abc-123", command: "npm run build", started_at: 1_700_000_000, elapsed: 120 },
          { handle_id: "def-456", command: "rspec spec/integration", started_at: 1_700_000_100, elapsed: 20 }
        ]
      )

      expect(events.size).to eq(1)
      e = events.first[:ev]
      expect(e[:type]).to eq("background_tasks_update")
      expect(e[:session_id]).to eq(session_id)
      expect(e[:running]).to eq(2)
      expect(e[:tasks].size).to eq(2)
      expect(e[:tasks].first[:handle_id]).to eq("abc-123")
      expect(e[:tasks].first[:command]).to eq("npm run build")
      expect(e[:tasks].first[:elapsed]).to eq(120)
    end

    it "truncates long commands to 80 chars" do
      long_cmd = "x" * 200
      controller.update_background_tasks(
        running: 1,
        tasks: [{ handle_id: "zzz", command: long_cmd, elapsed: 5 }]
      )
      cmd = events.first[:ev][:tasks].first[:command]
      expect(cmd.length).to eq(81)
      expect(cmd).to end_with("…")
    end

    it "handles empty tasks (hides badge)" do
      controller.update_background_tasks(running: 0, tasks: [])
      e = events.first[:ev]
      expect(e[:running]).to eq(0)
      expect(e[:tasks]).to eq([])
    end

    it "handles string-keyed task hashes" do
      controller.update_background_tasks(
        running: 1,
        tasks: [{ "handle_id" => "t1", "command" => "echo hi", "elapsed" => 3 }]
      )
      t = events.first[:ev][:tasks].first
      expect(t[:handle_id]).to eq("t1")
      expect(t[:command]).to eq("echo hi")
      expect(t[:elapsed]).to eq(3)
    end

    it "does not forward to channel subscribers" do
      sub = double("subscriber")
      controller.subscribe_channel(sub)
      expect(sub).not_to receive(:update_background_tasks)
      controller.update_background_tasks(running: 1, tasks: [])
    end
  end

  describe "#show_background_task_notice" do
    it "emits background_task_notice with command, handle_id and status" do
      controller.show_background_task_notice(
        command: "npm run build",
        handle_id: "abc-123",
        status: "success"
      )

      e = events.first[:ev]
      expect(e[:type]).to eq("background_task_notice")
      expect(e[:session_id]).to eq(session_id)
      expect(e[:command]).to eq("npm run build")
      expect(e[:handle_id]).to eq("abc-123")
      expect(e[:status]).to eq("success")
    end

    it "truncates long commands to 60 chars" do
      long_cmd = "y" * 200
      controller.show_background_task_notice(command: long_cmd, handle_id: "x", status: "failed")
      cmd = events.first[:ev][:command]
      expect(cmd.length).to eq(61)
      expect(cmd).to end_with("…")
    end

    it "handles nil command and handle_id" do
      controller.show_background_task_notice(command: nil, handle_id: nil, status: "error")
      e = events.first[:ev]
      expect(e[:command]).to eq("")
      expect(e[:handle_id]).to eq("")
      expect(e[:status]).to eq("error")
    end

    it "does not forward to channel subscribers" do
      sub = double("subscriber")
      controller.subscribe_channel(sub)
      expect(sub).not_to receive(:show_background_task_notice)
      controller.show_background_task_notice(command: "x", handle_id: "y", status: "success")
    end
  end

  describe "#update_user_message_queue_status" do
    it "emits user_message_queue_status with pending count" do
      controller.update_user_message_queue_status(pending: 3)
      e = events.first[:ev]
      expect(e[:type]).to eq("user_message_queue_status")
      expect(e[:session_id]).to eq(session_id)
      expect(e[:pending]).to eq(3)
    end

    it "coerces pending to integer" do
      controller.update_user_message_queue_status(pending: "5")
      expect(events.first[:ev][:pending]).to eq(5)
    end

    it "does not forward to channel subscribers" do
      sub = double("subscriber")
      controller.subscribe_channel(sub)
      expect(sub).not_to receive(:update_user_message_queue_status)
      controller.update_user_message_queue_status(pending: 1)
    end
  end
end
