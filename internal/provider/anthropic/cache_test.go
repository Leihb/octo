package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
)

func TestCacheableRequest_SystemBreakpoint(t *testing.T) {
	var body apiRequest
	tools := toAPITools([]agent.ToolDefinition{{Name: "t1"}, {Name: "t2"}})
	cacheableRequest(&body, "you are octo", tools)

	sysJSON, _ := json.Marshal(body.System)
	if !strings.Contains(string(sysJSON), `"ephemeral"`) {
		t.Errorf("system should carry an ephemeral breakpoint: %s", sysJSON)
	}
	// With a system prompt anchoring the breakpoint, tools must NOT be marked
	// (the system breakpoint already caches tools+system).
	for i, tl := range body.Tools {
		if tl.CacheControl != nil {
			t.Errorf("tool[%d] should not be marked when system carries the breakpoint", i)
		}
	}
}

func TestCacheableRequest_ToolFallbackWhenNoSystem(t *testing.T) {
	var body apiRequest
	tools := toAPITools([]agent.ToolDefinition{{Name: "t1"}, {Name: "t2"}})
	cacheableRequest(&body, "", tools)

	if body.System != nil {
		t.Errorf("empty system should serialize as nil/omitted, got %v", body.System)
	}
	// Last tool gets the breakpoint so the tools array still caches.
	if body.Tools[len(body.Tools)-1].CacheControl == nil {
		t.Errorf("last tool should carry the fallback breakpoint")
	}
	if body.Tools[0].CacheControl != nil {
		t.Errorf("only the last tool should be marked, not the first")
	}
}

func TestCacheableRequest_NoSystemNoTools(t *testing.T) {
	var body apiRequest
	cacheableRequest(&body, "", nil)
	if body.System != nil {
		t.Errorf("system should be nil")
	}
	if body.Tools != nil {
		t.Errorf("tools should be nil")
	}
}

func TestMarkMessageCacheable_StringContent(t *testing.T) {
	m := apiMessage{Role: "user", Content: json.RawMessage(`"hello world"`)}
	out := markMessageCacheable(m)
	var blocks []map[string]any
	if err := json.Unmarshal(out.Content, &blocks); err != nil {
		t.Fatalf("string content should become a block array: %v (%s)", err, out.Content)
	}
	if len(blocks) != 1 || blocks[0]["text"] != "hello world" || blocks[0]["type"] != "text" {
		t.Errorf("expected one text block carrying the string, got %v", blocks)
	}
	if blocks[0]["cache_control"] == nil {
		t.Errorf("text block should carry cache_control: %v", blocks[0])
	}
}

func TestMarkMessageCacheable_BlockArrayContent(t *testing.T) {
	m := apiMessage{Role: "assistant", Content: json.RawMessage(
		`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)}
	out := markMessageCacheable(m)
	var blocks []map[string]any
	if err := json.Unmarshal(out.Content, &blocks); err != nil {
		t.Fatal(err)
	}
	if blocks[0]["cache_control"] != nil {
		t.Errorf("only the LAST block should be marked; first was marked: %v", blocks[0])
	}
	if blocks[len(blocks)-1]["cache_control"] == nil {
		t.Errorf("last block should carry cache_control: %v", blocks[len(blocks)-1])
	}
}

func TestMarkMessagesCacheable_LastTwo(t *testing.T) {
	msgs := []apiMessage{
		{Role: "user", Content: json.RawMessage(`"m0"`)},
		{Role: "assistant", Content: json.RawMessage(`"m1"`)},
		{Role: "user", Content: json.RawMessage(`"m2"`)},
	}
	markMessagesCacheable(msgs)
	// m0 untouched (still a bare string), m1 + m2 marked.
	if string(msgs[0].Content) != `"m0"` {
		t.Errorf("m0 should be untouched, got %s", msgs[0].Content)
	}
	for _, i := range []int{1, 2} {
		if !strings.Contains(string(msgs[i].Content), `"ephemeral"`) {
			t.Errorf("m%d should carry a cache breakpoint: %s", i, msgs[i].Content)
		}
	}
}

func TestMarkMessagesCacheable_SingleMessage(t *testing.T) {
	msgs := []apiMessage{{Role: "user", Content: json.RawMessage(`"only"`)}}
	markMessagesCacheable(msgs)
	if !strings.Contains(string(msgs[0].Content), `"ephemeral"`) {
		t.Errorf("the single message should be marked: %s", msgs[0].Content)
	}
}

func TestCacheableRequest_MarksHistory(t *testing.T) {
	body := apiRequest{Messages: []apiMessage{
		{Role: "user", Content: json.RawMessage(`"u0"`)},
		{Role: "assistant", Content: json.RawMessage(`"a0"`)},
	}}
	cacheableRequest(&body, "sys", nil)
	// Both messages (last two of two) carry breakpoints; system too.
	for i := range body.Messages {
		if !strings.Contains(string(body.Messages[i].Content), `"ephemeral"`) {
			t.Errorf("message %d should be cache-marked: %s", i, body.Messages[i].Content)
		}
	}
}
