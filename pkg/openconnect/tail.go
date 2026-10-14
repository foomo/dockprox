package openconnect

import (
	"bytes"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
)

// tailLines is how much openconnect output an error message carries.
const tailLines = 10

// tail logs openconnect's output line by line and keeps the last few lines
// for error messages, which is where openconnect explains a failed login.
type tail struct {
	mu    sync.Mutex
	buf   []byte
	lines []string
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, b...)

	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}

		line := strings.TrimSpace(string(t.buf[:i]))
		t.buf = t.buf[i+1:]

		if line == "" {
			continue
		}

		log.Info("openconnect", "out", line)

		t.lines = append(t.lines, line)
		if len(t.lines) > tailLines {
			t.lines = t.lines[1:]
		}
	}

	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	lines := t.lines
	if rest := strings.TrimSpace(string(t.buf)); rest != "" {
		lines = append(slices.Clip(lines), rest)
	}

	return strings.Join(lines, " | ")
}
