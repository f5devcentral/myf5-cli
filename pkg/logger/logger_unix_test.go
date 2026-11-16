//go:build !windows

package logger

import "testing"

func TestSyslogLoggerClose(t *testing.T) {
	slNil := &SyslogLogger{writer: nil}
	if err := slNil.Close(); err != nil {
		t.Errorf("SyslogLogger.Close() with nil writer error: %v", err)
	}
}

func TestSyslogLoggerMethods(t *testing.T) {
	sl := &SyslogLogger{}
	sl.Info("test info with token %s", "Bearer token123")
	sl.Warn("test warn with password %s", "secretpass")
	sl.Error("test error with url %s", "https://admin:pass@host")
}
