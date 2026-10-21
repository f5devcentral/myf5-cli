package logger

import (
	"fmt"
	"io"
	"log"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

var (
	bearerRegex   = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9_\-\.]+`)
	basicRegex    = regexp.MustCompile(`(?i)(basic\s+)[A-Za-z0-9+/=]+`)
	kvSecretRegex = regexp.MustCompile(`(?i)(["']?(?:client_secret|client-secret|password|secret|token|api[_-]?key)["']?\s*[:=]\s*["'])([^"',\s]+)(["']?)`)
)

// Logger is the unified logging interface for system events.
type Logger interface {
	Info(format string, args ...any)
	Warn(format string, args ...any)
	Error(format string, args ...any)
	Close() error
}

var (
	defaultLogger Logger
	once          sync.Once
)

// Get returns the process-wide default OS logger.
func Get() Logger {
	once.Do(func() {
		defaultLogger = newOSLogger()
	})
	return defaultLogger
}

// ScrubSecrets sanitizes cleartext credentials, authorization tokens, and secrets from log strings.
func ScrubSecrets(msg string) string {
	// 1. Scrub Bearer tokens
	s := bearerRegex.ReplaceAllString(msg, "${1}[REDACTED]")

	// 2. Scrub Basic auth tokens
	s = basicRegex.ReplaceAllString(s, "${1}[REDACTED]")

	// 3. Scrub key-value pairs (JSON or query/form params)
	s = kvSecretRegex.ReplaceAllString(s, "${1}[REDACTED]${3}")

	// 4. Scrub embedded credentials in URLs (e.g. https://admin:password@host)
	if strings.Contains(s, "://") && strings.Contains(s, "@") {
		s = scrubURLCredentials(s)
	}

	return s
}

// scrubURLCredentials parses and redacts credentials in URLs embedded in text.
func scrubURLCredentials(msg string) string {
	words := strings.Fields(msg)
	for i, w := range words {
		if strings.Contains(w, "://") && strings.Contains(w, "@") {
			if u, err := url.Parse(w); err == nil && u.User != nil {
				u.User = url.UserPassword(u.User.Username(), "REDACTED")
				words[i] = u.String()
			}
		}
	}
	return strings.Join(words, " ")
}

// FallbackLogger writes sanitized messages to an io.Writer (e.g. os.Stderr).
type FallbackLogger struct {
	std *log.Logger
}

// NewFallbackLogger creates a fallback logger writing to the given writer.
func NewFallbackLogger(w io.Writer) *FallbackLogger {
	return &FallbackLogger{
		std: log.New(w, "myf5: ", log.LstdFlags),
	}
}

func (l *FallbackLogger) Info(format string, args ...any) {
	raw := fmt.Sprintf(format, args...)
	l.std.Println("[INFO] " + ScrubSecrets(raw))
}

func (l *FallbackLogger) Warn(format string, args ...any) {
	raw := fmt.Sprintf(format, args...)
	l.std.Println("[WARN] " + ScrubSecrets(raw))
}

func (l *FallbackLogger) Error(format string, args ...any) {
	raw := fmt.Sprintf(format, args...)
	l.std.Println("[ERROR] " + ScrubSecrets(raw))
}

func (l *FallbackLogger) Close() error {
	return nil
}
