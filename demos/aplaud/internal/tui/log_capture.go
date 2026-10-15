package tui

import (
	"bytes"
	"io"
	"sync"
	"time"
)

type LogCapture struct {
	mu     sync.Mutex
	buf    *bytes.Buffer
	lines  []logLine
	maxLen int
	writer *logWriter
}

type logLine struct {
	Time    time.Time
	Content string
}

func NewLogCapture(maxLines int) *LogCapture {
	lc := &LogCapture{
		buf:    &bytes.Buffer{},
		maxLen: maxLines,
	}
	lc.writer = &logWriter{lc: lc}
	return lc
}

func (lc *LogCapture) Write(p []byte) (int, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	n, err := lc.buf.Write(p)
	if err != nil {
		return n, err
	}

	for {
		line, err := lc.buf.ReadString('\n')
		if err != nil {
			if len(line) > 0 {
				lc.buf.WriteString(line)
			}
			break
		}

		ll := logLine{
			Time:    time.Now(),
			Content: line,
		}
		lc.lines = append(lc.lines, ll)
		if len(lc.lines) > lc.maxLen {
			lc.lines = lc.lines[len(lc.lines)-lc.maxLen:]
		}
	}

	return n, nil
}

func (lc *LogCapture) Writer() io.Writer {
	return lc.writer
}

func (lc *LogCapture) Lines() []logLine {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	out := make([]logLine, len(lc.lines))
	copy(out, lc.lines)
	return out
}

func (lc *LogCapture) LineCount() int {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return len(lc.lines)
}

func (lc *LogCapture) Tail(n int) []logLine {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if n > len(lc.lines) {
		n = len(lc.lines)
	}
	out := make([]logLine, n)
	copy(out, lc.lines[len(lc.lines)-n:])
	return out
}

type logWriter struct {
	lc *LogCapture
}

func (w *logWriter) Write(p []byte) (int, error) {
	return w.lc.Write(p)
}
