//go:build windows

package logger

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc/eventlog"
)

type EventLogLogger struct {
	elog *eventlog.Log
}

func newOSLogger() Logger {
	const src = "myf5"
	elog, err := eventlog.Open(src)
	if err != nil {
		// Event source might not be installed; fallback to stderr
		return NewFallbackLogger(os.Stderr)
	}
	return &EventLogLogger{elog: elog}
}

func (l *EventLogLogger) Info(format string, args ...any) {
	msg := ScrubSecrets(fmt.Sprintf(format, args...))
	_ = l.elog.Info(1001, msg)
}

func (l *EventLogLogger) Warn(format string, args ...any) {
	msg := ScrubSecrets(fmt.Sprintf(format, args...))
	_ = l.elog.Warning(1002, msg)
}

func (l *EventLogLogger) Error(format string, args ...any) {
	msg := ScrubSecrets(fmt.Sprintf(format, args...))
	_ = l.elog.Error(1003, msg)
}

func (l *EventLogLogger) Close() error {
	if l.elog != nil {
		return l.elog.Close()
	}
	return nil
}
