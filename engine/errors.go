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
	code := "unknown_error"
	help := "omaseal doctor"
	var oerr *omasealError
	if errors.As(err, &oerr) {
		code = oerr.code
		help = oerr.help
	}
	fmt.Fprintf(os.Stderr, "error: %s (code: %s, help: %s)\n", prefix+err.Error(), code, help)
}
