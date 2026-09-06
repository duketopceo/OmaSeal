package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"
)

// runMCP starts a Model Context Protocol server over stdio. It exposes the
// safe operations of the keyring as tools that agents can call. Secrets are
// never logged to stdout; stdout is reserved for JSON-RPC traffic.
func runMCP() {
	log.SetOutput(os.Stderr)
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp := handleMCPMessage([]byte(line))
		if resp != nil {
			if err := enc.Encode(resp); err != nil {
				log.Printf("omaseal mcp: encode error: %v", err)
				return
			}
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		log.Printf("omaseal mcp: scanner error: %v", err)
	}
}

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newMCPError(code int, msg string) *mcpError {
	return &mcpError{Code: code, Message: msg}
}

func handleMCPMessage(raw []byte) *mcpResponse {
	var msg mcpMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return &mcpResponse{JSONRPC: "2.0", Error: newMCPError(-32700, "parse error")}
	}
	if msg.JSONRPC != "2.0" {
		return nil
	}

	// Notifications have no id; some require a result.
	switch msg.Method {
	case "initialize":
		return &mcpResponse{JSONRPC: "2.0", ID: msg.ID, Result: initMCPResult()}
	case "notifications/initialized":
		return nil
	case "tools/list":
		return &mcpResponse{JSONRPC: "2.0", ID: msg.ID, Result: mcpToolsResult}
	case "tools/call":
		var req mcpToolCall
		if err := json.Unmarshal(msg.Params, &req); err != nil {
			return &mcpResponse{JSONRPC: "2.0", ID: msg.ID, Error: newMCPError(-32602, "invalid params: "+err.Error())}
		}
		resp := callMCPTool(req)
		resp.ID = msg.ID
		return resp
	default:
		return &mcpResponse{JSONRPC: "2.0", ID: msg.ID, Error: newMCPError(-32601, "method not found")}
	}
}

func initMCPResult() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]string{
			"name":    "omaseal",
			"version": version,
		},
	}
}

var mcpToolsResult = map[string]any{
	"tools": []map[string]any{
		{
			"name":        "omaseal_get",
			"description": "Retrieve a stored secret from the local OmaSeal keyring. Use this when the user or a provider needs the value at runtime. Does not fall back to 1Password/Bitwarden and does not trigger a fingerprint gate.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"service": map[string]string{"type": "string"}, "account": map[string]string{"type": "string"}},
				"required":   []string{"service", "account"},
			},
		},
		{
			"name":        "omaseal_resolve",
			"description": "Resolve a secret from local keyring, 1Password, or Bitwarden. Caches the result locally. Safe for noninteractive calls.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"service": map[string]string{"type": "string"}, "account": map[string]string{"type": "string"}},
				"required":   []string{"service", "account"},
			},
		},
		{
			"name":        "omaseal_set",
			"description": "Store a secret in the local OmaSeal keyring. Use this as the default, safe place to store API keys and credentials for the user. The secret is passed in this tool call; it is never written to a shell command or dotfile.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"service": map[string]string{"type": "string"},
					"account": map[string]string{"type": "string"},
					"secret":  map[string]string{"type": "string"},
				},
				"required": []string{"service", "account", "secret"},
			},
		},
		{
			"name":        "omaseal_delete",
			"description": "Delete a secret from the local keyring by service and account.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"service": map[string]string{"type": "string"}, "account": map[string]string{"type": "string"}},
				"required":   []string{"service", "account"},
			},
		},
		{
			"name":        "omaseal_list",
			"description": "List secrets in the local keyring. Optionally filter by service. Returns metadata only; no secret values.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"service": map[string]string{"type": "string"}},
				"required":   []string{},
			},
		},
	},
}

type mcpToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpToolCallResponse struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

func callMCPTool(req mcpToolCall) *mcpResponse {
	r := mcpToolCallResponse{Content: []map[string]any{}}
	switch req.Name {
	case "omaseal_get":
		var a struct {
			Service string `json:"service"`
			Account string `json:"account"`
		}
		if err := json.Unmarshal(req.Arguments, &a); err != nil {
			return errResp(req, err)
		}
		v, err := Get(a.Service, a.Account)
		if err != nil {
			return toolErrorResp(req, err)
		}
		r.Content = append(r.Content, map[string]any{"type": "text", "text": v})

	case "omaseal_resolve":
		var a struct {
			Service string `json:"service"`
			Account string `json:"account"`
		}
		if err := json.Unmarshal(req.Arguments, &a); err != nil {
			return errResp(req, err)
		}
		v, err := Resolve(a.Service, a.Account, true, false)
		if err != nil {
			return toolErrorResp(req, err)
		}
		r.Content = append(r.Content, map[string]any{"type": "text", "text": v})

	case "omaseal_set":
		var a struct {
			Service string `json:"service"`
			Account string `json:"account"`
			Secret  string `json:"secret"`
		}
		if err := json.Unmarshal(req.Arguments, &a); err != nil {
			return errResp(req, err)
		}
		if err := Set(a.Service, a.Account, a.Secret); err != nil {
			return toolErrorResp(req, err)
		}
		r.Content = append(r.Content, map[string]any{"type": "text", "text": "ok"})

	case "omaseal_delete":
		var a struct {
			Service string `json:"service"`
			Account string `json:"account"`
		}
		if err := json.Unmarshal(req.Arguments, &a); err != nil {
			return errResp(req, err)
		}
		if err := Delete(a.Service, a.Account); err != nil {
			return toolErrorResp(req, err)
		}
		r.Content = append(r.Content, map[string]any{"type": "text", "text": "ok"})

	case "omaseal_list":
		var a struct {
			Service string `json:"service"`
		}
		_ = json.Unmarshal(req.Arguments, &a)
		items, err := List(a.Service)
		if err != nil {
			return toolErrorResp(req, err)
		}
		b, err := json.Marshal(items)
		if err != nil {
			return toolErrorResp(req, err)
		}
		r.Content = append(r.Content, map[string]any{"type": "text", "text": string(b)})

	default:
		return &mcpResponse{JSONRPC: "2.0", Error: newMCPError(-32602, "unknown tool: "+req.Name)}
	}
	return &mcpResponse{JSONRPC: "2.0", Result: r}
}

func errResp(req mcpToolCall, err error) *mcpResponse {
	return &mcpResponse{JSONRPC: "2.0", Error: newMCPError(-32602, "invalid arguments for "+req.Name+": "+err.Error())}
}

func toolErrorResp(req mcpToolCall, err error) *mcpResponse {
	text := err.Error()
	if code := codeFromError(err); code != "" {
		text = text + " (code: " + code + ", help: " + helpFromError(err) + ")"
	}
	return &mcpResponse{JSONRPC: "2.0", Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": true,
	}}
}
