package main

type pingCheck struct {
	Name     string `json:"name"`
	Ok       bool   `json:"ok"`
	Optional bool   `json:"optional,omitempty"`
	Message  string `json:"message"`
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
	emitCheckJSON(doctorChecks(), "omaseal doctor")
}
