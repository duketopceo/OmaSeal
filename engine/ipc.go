package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ipcRequest holds the supported payload shapes.
// The secret value is intentionally not here; set must go through the CLI
// with stdin, never through shell arguments or JSON payloads.
type ipcRequest struct {
	Service string `json:"service"`
	Account string `json:"account"`
	Sort    string `json:"sort"`
}

type ipcResponse struct {
	OK     string           `json:"ok,omitempty"`
	Secret string           `json:"secret,omitempty"`
	Items  []Item           `json:"items,omitempty"`
	Stats  *AnalyticsReport `json:"stats,omitempty"`
	Error  string           `json:"error,omitempty"`
	Code   string           `json:"code,omitempty"`
	Help   string           `json:"help,omitempty"`
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
		service, account, err := refAwareCredentials(req.Service, req.Account, false)
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		secret, err := Get(service, account)
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		WriteLog("access %s/%s", service, account)
		resp.Secret = secret

	case "set":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			resp.Code = "invalid_json"
			resp.Help = "omaseal ipc set '{\"service\":\"...\",\"account\":\"...\"}' < secret.txt"
			writeJSON(resp)
			os.Exit(1)
		}
		// Writes are strict: new names must satisfy the shared charset.
		service, account, verr := refAwareCredentials(req.Service, req.Account, true)
		if verr != nil {
			resp.Error = verr.Error()
			resp.Code = codeFromError(verr)
			resp.Help = helpFromError(verr)
			writeJSON(resp)
			os.Exit(1)
		}
		// The secret arrives on stdin — never inside the JSON payload. A TTY
		// stdin would turn the read into an interactive prompt on a terminal
		// the IPC caller may not own; a held-open pipe would block forever —
		// both are rejected with a typed error instead of hanging.
		if isStdinTTY() {
			resp.Error = "ipc set requires a piped secret on stdin"
			resp.Code = "invalid_secret"
			resp.Help = "omaseal ipc set '{\"service\":\"...\",\"account\":\"...\"}' < secret.txt"
			writeJSON(resp)
			os.Exit(1)
		}
		secret, err := readSecretDeadline(30 * time.Second)
		if err != nil || secret == "" {
			resp.Error = "reading secret from stdin"
			if err != nil {
				resp.Error += ": " + err.Error()
			}
			resp.Code = "invalid_secret"
			resp.Help = "omaseal ipc set '{\"service\":\"...\",\"account\":\"...\"}' < secret.txt"
			writeJSON(resp)
			os.Exit(1)
		}
		if err := Set(service, account, secret); err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.OK = "ok"

	case "del", "delete":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			resp.Code = "invalid_json"
			resp.Help = "omaseal ipc del '{\"service\":\"...\",\"account\":\"...\"}'"
			writeJSON(resp)
			os.Exit(1)
		}
		service, account, verr := refAwareCredentials(req.Service, req.Account, false)
		if verr != nil {
			resp.Error = verr.Error()
			resp.Code = codeFromError(verr)
			resp.Help = helpFromError(verr)
			writeJSON(resp)
			os.Exit(1)
		}
		if err := Delete(service, account); err != nil {
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
		service, serr := refAwareService(req.Service, false)
		if serr != nil {
			resp.Error = serr.Error()
			resp.Code = "invalid_name"
			resp.Help = "omaseal ipc list '{\"service\":\"...\"}'"
			writeJSON(resp)
			os.Exit(1)
		}
		items, err := listWithUsage(service, req.Sort)
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Items = items

	case "stats", "analytics":
		report, err := GetAnalyticsReport()
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		resp.Stats = report

	case "resolve":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			resp.Error = "invalid json: " + err.Error()
			resp.Code = "invalid_json"
			resp.Help = "omaseal ipc resolve '{\"service\":\"...\",\"account\":\"...\"}'"
			writeJSON(resp)
			os.Exit(1)
		}
		service, account, verr := refAwareCredentials(req.Service, req.Account, false)
		if verr != nil {
			resp.Error = verr.Error()
			resp.Code = codeFromError(verr)
			resp.Help = helpFromError(verr)
			writeJSON(resp)
			os.Exit(1)
		}
		secret, err := Resolve(context.Background(), service, account, true, false)
		if err != nil {
			resp.Error = err.Error()
			resp.Code = codeFromError(err)
			resp.Help = helpFromError(err)
			writeJSON(resp)
			os.Exit(1)
		}
		WriteLog("access %s/%s", service, account)
		resp.Secret = secret

	default:
		resp.Error = "unknown method: " + method
		resp.Code = "unknown_method"
		resp.Help = "omaseal ipc ping|get|set|del|list|stats|resolve"
		writeJSON(resp)
		os.Exit(1)
	}

	writeJSON(resp)
}

func writeJSON(v interface{}) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}
