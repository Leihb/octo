package mcp

import (
	"encoding/json"
	"testing"
)

func TestMessage_IsRequest(t *testing.T) {
	m := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if !m.IsRequest() {
		t.Error("expected IsRequest=true")
	}
	if m.IsNotification() {
		t.Error("expected IsNotification=false")
	}
	if m.IsResponse() {
		t.Error("expected IsResponse=false")
	}
}

func TestMessage_IsNotification(t *testing.T) {
	m := &Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if !m.IsNotification() {
		t.Error("expected IsNotification=true")
	}
	if m.IsRequest() {
		t.Error("expected IsRequest=false")
	}
	if m.IsResponse() {
		t.Error("expected IsResponse=false")
	}
}

func TestMessage_IsResponse(t *testing.T) {
	m := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`)}
	if !m.IsResponse() {
		t.Error("expected IsResponse=true")
	}
	if m.IsRequest() {
		t.Error("expected IsRequest=false")
	}
}

func TestMessage_MarshalShape(t *testing.T) {
	// Request: id + method present, result/error/params optional.
	m := &Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"echo"}`),
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo"}}`
	if string(b) != want {
		t.Errorf("marshal:\n  got  %s\n  want %s", b, want)
	}
}

func TestRPCError_ErrorReturnsMessage(t *testing.T) {
	e := &RPCError{Code: -32601, Message: "Method not found"}
	if e.Error() != "Method not found" {
		t.Errorf("Error() = %q", e.Error())
	}
	var nilErr *RPCError
	if nilErr.Error() != "" {
		t.Errorf("nil RPCError.Error() should be empty")
	}
}

func TestProtocolVersion_IsSpecRevision(t *testing.T) {
	// Sanity guard against accidental version bumps. If we ever upgrade
	// this string the test should flag the change so it's intentional.
	if ProtocolVersion != "2024-11-05" {
		t.Errorf("ProtocolVersion = %q, expected the documented 2024-11-05 spec", ProtocolVersion)
	}
}
