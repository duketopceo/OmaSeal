package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type pingCheck struct {
	Name    string `json:"name"`
	Ok      bool   `json:"ok"`
	Message string `json:"message"`
}

type pingResult struct {
	Version string      `json:"version"`
	Commit  string      `json:"commit"`
	Target  string      `json:"target"`
	Ok      bool        `json:"ok"`
	Checks  []pingCheck `json:"checks"`
	Help    string      `json:"help"`
}

func handlePing() {
	runPing()
}

func runPing() {
	results := doctorChecks()
	checks := make([]pingCheck, len(results))
	ok := true
	for i, r := range results {
		checks[i] = pingCheck{Name: r.name, Ok: r.ok, Message: r.message}
		if !r.ok {
			ok = false
		}
	}

	res := pingResult{
		Version: version,
		Commit:  commit,
		Target:  target(),
		Ok:      ok,
		Checks:  checks,
		Help:    "omaseal doctor",
	}

	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error marshaling ping result:", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}
