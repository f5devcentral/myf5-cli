//go:build windows

package logger

import "testing"

func TestEventLogLoggerCloseNil(t *testing.T) {
	l := &EventLogLogger{}
	if err := l.Close(); err != nil {
		t.Errorf("EventLogLogger.Close() with nil elog error: %v", err)
	}
}

func TestNewOSLoggerWindows(t *testing.T) {
	l := newOSLogger()
	if l == nil {
		t.Fatal("expected non-nil logger from newOSLogger()")
	}
	l.Info("test info with token %s", "Bearer abc123")
	l.Warn("test warn with password %s", "password=hunter2")
	l.Error("test error with token %s and secret %s", "Bearer abc123", "password=hunter2")
	if err := l.Close(); err != nil {
		t.Errorf("expected l.Close() to return nil, got: %v", err)
	}
}
