package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

var (
	logInitOnce sync.Once
	logFile     *os.File
	logFilePath string
	logWriter   io.Writer
	logInitErr  error
)

// LogPath returns the resolved path to the omaseal log file, creating the
// parent directory if needed. It is safe to call before the log file is opened.
func LogPath() string {
	logInitOnce.Do(initLog)
	return logFilePath
}

func ensureLogDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "omaseal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func initLog() {
	dir, err := ensureLogDir()
	if err != nil {
		logInitErr = err
		logWriter = os.Stderr
		return
	}
	logFilePath = filepath.Join(dir, "omaseal.log")
	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		logInitErr = err
		logWriter = os.Stderr
		return
	}
	logFile = f
	logWriter = io.MultiWriter(os.Stderr, logFile)
}

// LogWriter returns the multi-writer used by OmaSeal. It initializes the log
// file on first call and falls back to stderr if initialization fails.
func LogWriter() io.Writer {
	logInitOnce.Do(initLog)
	return logWriter
}

// SetLogOutput redirects the standard log package to write to both stderr and
// the omaseal log file. Call once near process start.
func SetLogOutput() {
	log.SetOutput(LogWriter())
}

// WriteLog records a single event without secret values.
func WriteLog(format string, v ...any) {
	log.Printf(format, v...)
}

// ReadLog returns the last n lines from the omaseal log file. If the file
// does not exist, it returns an empty slice.
func ReadLog(n int) ([]string, error) {
	if n <= 0 {
		n = 50
	}
	path := LogPath()
	if path == "" {
		return nil, fmt.Errorf("cannot determine log path: %v", logInitErr)
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	lines := make([]string, 0, n)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}
