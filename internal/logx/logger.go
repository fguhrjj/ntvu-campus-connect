// Package logx writes the redacted diagnostic log (spec §8).
package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MaxSize triggers rotation; one previous generation (<path>.1) is kept.
const MaxSize = 1 << 20

// Logger appends timestamped, redacted lines. Safe for concurrent use.
type Logger struct {
	mu       sync.Mutex
	path     string
	f        *os.File
	size     int64
	username string
}

// NewLogger opens path for appending, creating its directory.
func NewLogger(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &Logger{path: path}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

// Path is the current log file.
func (l *Logger) Path() string { return l.path }

func (l *Logger) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

// SetUsername registers the account so its full value is masked everywhere.
func (l *Logger) SetUsername(u string) {
	l.mu.Lock()
	l.username = strings.TrimSpace(u)
	l.mu.Unlock()
}

// Request records one HTTP request. target is host+path; any query is dropped.
func (l *Logger) Request(method, target string, status int, elapsed time.Duration) {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	l.write(fmt.Sprintf("%s %s %d %dms", method, target, status, elapsed.Milliseconds()))
}

// Event records a free-form line after redaction.
func (l *Logger) Event(message string) { l.write(message) }

// Eventf formats then records a line after redaction.
func (l *Logger) Eventf(format string, args ...any) { l.write(fmt.Sprintf(format, args...)) }

func (l *Logger) write(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	line := time.Now().Format("2006-01-02 15:04:05.000 ") + l.redact(msg) + "\n"
	if l.size > 0 && l.size+int64(len(line)) > MaxSize {
		l.rotate()
	}
	n, _ := l.f.WriteString(line)
	l.size += int64(n)
}

func (l *Logger) rotate() {
	l.f.Close()
	l.f = nil
	_ = os.Remove(l.path + ".1")
	_ = os.Rename(l.path, l.path+".1")
	if err := l.open(); err != nil {
		l.f = nil
	}
}

// Close flushes and closes the file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// sensitive names whose values are never logged (spec §8). Matched
// case-insensitively as query/form pairs, key=value text and JSON members.
const sensitive = `sessionid|flowsessionid|userindex|password|passwordtext|pwdencryptsalt|salt|execution|ticket|delegatedclientid|service|lt|jsessionid|session|pac4jdelsession|route|castgc|cookie`

var (
	pairRE   = regexp.MustCompile(`(?i)\b(` + sensitive + `)=([^&\s;"',]*)`)
	jsonRE   = regexp.MustCompile(`(?i)"(` + sensitive + `)"\s*:\s*"[^"]*"`)
	cookieRE = regexp.MustCompile(`(?i)\b(set-cookie|cookie):[^\n]*`)
)

func (l *Logger) redact(s string) string {
	s = cookieRE.ReplaceAllString(s, "$1: ***")
	s = jsonRE.ReplaceAllString(s, `"$1":"***"`)
	s = pairRE.ReplaceAllString(s, "$1=***")
	if l.username != "" {
		s = strings.ReplaceAll(s, l.username, MaskUsername(l.username))
	}
	return s
}

// MaskUsername keeps only the last 4 characters.
func MaskUsername(u string) string {
	r := []rune(u)
	if len(r) == 0 {
		return ""
	}
	if len(r) <= 4 {
		return "***"
	}
	return "***" + string(r[len(r)-4:])
}
