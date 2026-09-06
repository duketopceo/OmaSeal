package main

import (
	"encoding/json"
	"fmt"
	"os"
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
			writeJSON(resp)
			os.Exit(1)
		}
		secret, err := Get(req.Service, req.Account)
		if err != nil {
			resp.Error = err.Error(); resp.Code = codeFromError(err); resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Secret = secret

	case "del", "delete":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			writeJSON(resp)
			os.Exit(1)
		}
		if err := Delete(req.Service, req.Account); err != nil {
			resp.Error = err.Error(); resp.Code = codeFromError(err); resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.OK = "ok"

	case "list":
		var req ipcRequest
		_ = json.Unmarshal([]byte(jsonArgs), &req) // empty is fine
		items, err := List(req.Service)
		if err != nil {
			resp.Error = err.Error(); resp.Code = codeFromError(err); resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Items = items

	case "resolve":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			writeJSON(resp)
			os.Exit(1)
		}
		secret, err := Resolve(req.Service, req.Account, true, false)
		if err != nil {
			resp.Error = err.Error(); resp.Code = codeFromError(err); resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Secret = secret

	default:
		resp.Error = "unknown method: " + method
		writeJSON(resp)
		os.Exit(1)
	}

	writeJSON(resp)
}

func writeJSON(v interface{}) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}
