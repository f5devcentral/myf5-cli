//go:build !windows

package logger

import (
	"fmt"
	"log/syslog"
	"os"
)

type SyslogLogger struct {
	writer *syslog.Writer
}

func newOSLogger() Logger {
	// Attempt to connect to local syslog / unified logging daemon
	w, err := syslog.New(syslog.LOG_INFO|syslog.LOG_USER, "myf5")
	if err != nil {
		// In minimal containers or environments without a running syslog daemon, fallback to stderr
		return NewFallbackLogger(os.Stderr)
	}
	return &SyslogLogger{writer: w}
}

func (l *SyslogLogger) Info(format string, args ...any) {
	msg := ScrubSecrets(fmt.Sprintf(format, args...))
	_ = l.writer.Info(msg)
}

func (l *SyslogLogger) Warn(format string, args ...any) {
	msg := ScrubSecrets(fmt.Sprintf(format, args...))
	_ = l.writer.Warning(msg)
}

func (l *SyslogLogger) Error(format string, args ...any) {
	msg := ScrubSecrets(fmt.Sprintf(format, args...))
	_ = l.writer.Err(msg)
}

func (l *SyslogLogger) Close() error {
	if l.writer != nil {
		return l.writer.Close()
	}
	return nil
}
