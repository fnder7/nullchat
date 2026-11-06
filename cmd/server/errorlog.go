package main

import (
	"io"
	"log"
	"log/slog"
	"regexp"
)

// net/http reports some failures with the client's address, for example
// "http: panic serving 203.0.113.7:51234: ...". The service promises not to log
// IP addresses, so every line of the server's error log is redacted first.
var addressPattern = regexp.MustCompile(`\[[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*(?:%[0-9A-Za-z._-]+)?\](?::\d+)?|\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)

func redactAddresses(line string) string { return addressPattern.ReplaceAllString(line, "<addr>") }

type redactingWriter struct{ next io.Writer }

func (w redactingWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.next, redactAddresses(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// newErrorLog returns a logger for http.Server.ErrorLog that strips client
// addresses before writing to out.
func newErrorLog(out io.Writer) *log.Logger { return log.New(redactingWriter{out}, "", 0) }

// serverErrorLog forwards the redacted lines to slog at warning level.
func serverErrorLog() *log.Logger {
	return newErrorLog(slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn).Writer())
}
