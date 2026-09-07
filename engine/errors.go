package main

import (
	"errors"
	"fmt"
	"os"
)

type omasealError struct {
	code string
	help string
	err  error
}

func (e *omasealError) Error() string {
	return e.err.Error()
}

func (e *omasealError) Unwrap() error {
	return e.err
}

func newError(code, help string, err error) error {
	return &omasealError{code: code, help: help, err: err}
}

func codeFromError(err error) string {
	var oerr *omasealError
	if errors.As(err, &oerr) {
		return oerr.code
	}
	return ""
}

func helpFromError(err error) string {
	var oerr *omasealError
	if errors.As(err, &oerr) {
		return oerr.help
	}
	return ""
}

func printError(prefix string, err error) {
	var oerr *omasealError
	if errors.As(err, &oerr) {
		fmt.Fprintf(os.Stderr, "error: %s (code: %s, help: %s)\n", prefix+err.Error(), oerr.code, oerr.help)
		return
	}
	fmt.Fprintln(os.Stderr, "error:", prefix+err.Error())
}
