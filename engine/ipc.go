package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ipcRequest holds the supported payload shapes.
// The secret value is intentionally not here; set must go through the CLI
// with stdin, never through shell arguments or JSON payloads.
type ipcRequest struct {
	Service string `json:"service"`
	Account string `json:"account"`
}

type ipcResponse struct {
	OK     string `json:"ok,omitempty"`
	Secret string `json:"secret,omitempty"`
	Items  []Item `json:"items,omitempty"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
	Help   string `json:"help,omitempty"`
}

func runIPC(method string, jsonArgs string) {
	var resp ipcResponse
	switch method {
	case "ping":
		resp.OK = "pong"

	case "get":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			resp.Code = "invalid_json"
			resp.Help = "omaseal ipc get '{\"service\":\"...\",\"account\":\"...\"}'"
			writeJSON(resp)
			os.Exit(1)
		}
		secret, err := Get(req.Service, req.Account)
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Secret = secret

	case "del", "delete":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			resp.Code = "invalid_json"
			resp.Help = "omaseal ipc del '{\"service\":\"...\",\"account\":\"...\"}'"
			writeJSON(resp)
			os.Exit(1)
		}
		if err := Delete(req.Service, req.Account); err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.OK = "ok"

	case "list":
		var req ipcRequest
		if s := strings.TrimSpace(jsonArgs); s != "" {
			if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
				resp.Error = "invalid json: " + err.Error()
				resp.Code = "invalid_json"
				resp.Help = "omaseal ipc list '{\"service\":\"...\"}'"
				writeJSON(resp)
				os.Exit(1)
			}
		}
		items, err := List(req.Service)
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Items = items

	case "resolve":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			resp.Code = "invalid_json"
			resp.Help = "omaseal ipc resolve '{\"service\":\"...\",\"account\":\"...\"}'"
			writeJSON(resp)
			os.Exit(1)
		}
		secret, err := Resolve(context.Background(), req.Service, req.Account, true, false)
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Secret = secret

	default:
		resp.Error = "unknown method: " + method
		resp.Code = "unknown_method"
		resp.Help = "omaseal ipc ping|get|del|list|resolve"
		writeJSON(resp)
		os.Exit(1)
	}

	writeJSON(resp)
}

func writeJSON(v interface{}) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}
