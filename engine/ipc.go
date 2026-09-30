package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// stdinIsTTY is the terminal-detection seam — tests stub it to drive both
// sides of the piped-secret check.
var stdinIsTTY = isStdinTTY

// listItems is the list seam — List() speaks DBus directly and ignores the
// keyring provider, so mock-keyring tests substitute the whole pipeline.
var listItems = listWithUsage

func runIPC(method string, jsonArgs string) {
	resp, code := ipcDispatch(method, jsonArgs, os.Stdin)
	writeJSON(resp)
	if code != 0 {
		os.Exit(code)
	}
}

// ipcDispatch is the testable core of `omaseal ipc` — it returns the
// response object and exit code instead of printing and exiting inline.
// stdin is injected so `set` can be tested without a real pipe.
func ipcDispatch(method string, jsonArgs string, stdin io.Reader) (ipcResponse, int) {
	var resp ipcResponse
	fail := func(err error, help string) (ipcResponse, int) {
		resp.Error = err.Error()
		resp.Code = codeFromError(err)
		resp.Help = help
		return resp, 1
	}
	failMsg := func(msg, code, help string) (ipcResponse, int) {
		resp.Error = msg
		resp.Code = code
		resp.Help = help
		return resp, 1
	}
	badJSON := func(err error, help string) (ipcResponse, int) {
		return failMsg("invalid json: "+err.Error(), "invalid_json", help)
	}

	switch method {
	case "ping":
		resp.OK = "pong"

	case "get":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			return badJSON(err, "omaseal ipc get '{\"service\":\"...\",\"account\":\"...\"}'")
		}
		service, account, err := refAwareCredentials(req.Service, req.Account, false)
		if err != nil {
			return fail(err, helpFromError(err))
		}
		secret, err := storeGet(service, account)
		if err != nil {
			return fail(err, helpFromError(err))
		}
		WriteLog("access %s/%s", service, account)
		resp.Secret = secret

	case "set":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			return badJSON(err, "omaseal ipc set '{\"service\":\"...\",\"account\":\"...\"}' < secret.txt")
		}
		// Writes are strict: new names must satisfy the shared charset.
		service, account, verr := refAwareCredentials(req.Service, req.Account, true)
		if verr != nil {
			return fail(verr, helpFromError(verr))
		}
		// The secret arrives on stdin — never inside the JSON payload. A TTY
		// stdin would turn the read into an interactive prompt on a terminal
		// the IPC caller may not own; a held-open pipe would block forever —
		// both are rejected with a typed error instead of hanging.
		if stdinIsTTY() {
			return failMsg("ipc set requires a piped secret on stdin",
				"invalid_secret",
				"omaseal ipc set '{\"service\":\"...\",\"account\":\"...\"}' < secret.txt")
		}
		secret, err := readSecretFrom(stdin, 30*time.Second)
		if err != nil || secret == "" {
			msg := "reading secret from stdin"
			if err != nil {
				msg += ": " + err.Error()
			}
			return failMsg(msg, "invalid_secret",
				"omaseal ipc set '{\"service\":\"...\",\"account\":\"...\"}' < secret.txt")
		}
		if err := storeSet(service, account, secret); err != nil {
			return fail(err, helpFromError(err))
		}
		resp.OK = "ok"

	case "del", "delete":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			return badJSON(err, "omaseal ipc del '{\"service\":\"...\",\"account\":\"...\"}'")
		}
		service, account, verr := refAwareCredentials(req.Service, req.Account, false)
		if verr != nil {
			return fail(verr, helpFromError(verr))
		}
		if err := storeDelete(service, account); err != nil {
			return fail(err, helpFromError(err))
		}
		resp.OK = "ok"

	case "list":
		var req ipcRequest
		if s := strings.TrimSpace(jsonArgs); s != "" {
			if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
				return badJSON(err, "omaseal ipc list '{\"service\":\"...\"}'")
			}
		}
		service, serr := refAwareService(req.Service, false)
		if serr != nil {
			return failMsg(serr.Error(), "invalid_name",
				"omaseal ipc list '{\"service\":\"...\"}'")
		}
		items, err := listItems(service, req.Sort)
		if err != nil {
			return fail(err, helpFromError(err))
		}
		resp.Items = items

	case "stats", "analytics":
		report, err := GetAnalyticsReport()
		if err != nil {
			return fail(err, helpFromError(err))
		}
		resp.Stats = report

	case "resolve":
		var req ipcRequest
		if err := json.Unmarshal([]byte(jsonArgs), &req); err != nil {
			return badJSON(err, "omaseal ipc resolve '{\"service\":\"...\",\"account\":\"...\"}'")
		}
		service, account, verr := refAwareCredentials(req.Service, req.Account, false)
		if verr != nil {
			return fail(verr, helpFromError(verr))
		}
		secret, err := Resolve(context.Background(), service, account, true, false)
		if err != nil {
			return fail(err, helpFromError(err))
		}
		WriteLog("access %s/%s", service, account)
		resp.Secret = secret

	default:
		return failMsg("unknown method: "+method, "unknown_method",
			"omaseal ipc ping|get|set|del|list|stats|resolve")
	}

	return resp, 0
}

func writeJSON(v interface{}) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}
