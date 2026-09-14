package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func callTool(t *testing.T, name, argsJSON string) *mcpResponse {
	t.Helper()
	setupAgentEnv(t) // default open policy: tools ungated
	resp := callMCPTool(mcpToolCall{Name: name, Arguments: json.RawMessage(argsJSON)})
	if resp == nil || resp.Error != nil {
		t.Fatalf("%s: protocol error %v", name, resp)
	}
	return resp
}

func toolResult(r *mcpResponse) mcpToolCallResponse {
	b, _ := json.Marshal(r.Result)
	var out mcpToolCallResponse
	_ = json.Unmarshal(b, &out)
	return out
}

func TestMCPStatusToolUngated(t *testing.T) {
	resp := callTool(t, "omaseal_status", `{}`)
	out := toolResult(resp)
	if out.IsError || len(out.Content) == 0 {
		t.Fatalf("status failed: %+v", out)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(out.Content[0]["text"].(string)), &d); err != nil {
		t.Fatalf("status not JSON: %v", out.Content[0])
	}
	if _, ok := d["mode"]; !ok {
		t.Fatalf("status missing mode: %v", d)
	}
}

func TestMCPStatusUngatedWhenLocked(t *testing.T) {
	setupAgentEnv(t)
	if err := saveAgentPolicy(AgentPolicy{Mode: "lock"}); err != nil {
		t.Fatal(err)
	}
	resp := callMCPTool(mcpToolCall{Name: "omaseal_status", Arguments: json.RawMessage(`{}`)})
	out := toolResult(resp)
	if out.IsError {
		t.Fatal("status must stay readable while locked")
	}
}

func TestMCPSetRejectsInvalidName(t *testing.T) {
	resp := callTool(t, "omaseal_set", `{"service":"bad svc","account":"a","secret":"s"}`)
	out := toolResult(resp)
	if !out.IsError {
		t.Fatal("off-charset service name accepted on write")
	}
	if !strings.Contains(out.Content[0]["text"].(string), "invalid_name") {
		t.Fatalf("want invalid_name code: %v", out.Content[0])
	}
}

func TestMCPGetAcceptsRef(t *testing.T) {
	// A verbatim omaseal:// reference in the service field parses; the lookup
	// itself fails (nothing stored) but must reach the keyring, not the parser.
	resp := callTool(t, "omaseal_get", `{"service":"omaseal://browseros/work/apiKey"}`)
	out := toolResult(resp)
	if out.IsError && strings.Contains(out.Content[0]["text"].(string), "invalid_name") {
		t.Fatalf("ref rejected at parse: %v", out.Content[0])
	}
}

func TestMCPGetRejectsRefWithAccount(t *testing.T) {
	resp := callTool(t, "omaseal_get", `{"service":"omaseal://s/a","account":"extra"}`)
	out := toolResult(resp)
	if !out.IsError {
		t.Fatal("ref + account accepted")
	}
}

func TestToolOperationExcludesStatus(t *testing.T) {
	// omaseal_status must not be routed through the agent gate.
	if toolOperation("omaseal_status") != "" {
		t.Fatal("status tool is gated")
	}
}
