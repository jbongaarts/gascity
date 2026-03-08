package convergence

import "unicode/utf8"

// CapturedOutput holds truncated stdout/stderr from a subprocess.
type CapturedOutput struct {
	Stdout    string
	Stderr    string
	Truncated bool // true if either was truncated
}

// MaxOutputBytes is the maximum size of captured gate stdout/stderr (4KB each).
const MaxOutputBytes = 4096

// boundedBuffer is an io.Writer that stores at most maxBytes.
// Once the limit is reached, further writes are silently discarded.
type boundedBuffer struct {
	buf      []byte
	maxBytes int
	overflow bool
}

func newBoundedBuffer(maxBytes int) *boundedBuffer {
	return &boundedBuffer{maxBytes: maxBytes}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.maxBytes - len(b.buf)
	if remaining <= 0 {
		b.overflow = true
		return len(p), nil // discard but report success to avoid breaking cmd
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.overflow = true
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte    { return b.buf }
func (b *boundedBuffer) Overflowed() bool { return b.overflow }

// TruncateOutput truncates a byte slice to maxBytes, returning the string
// and whether truncation occurred.
func TruncateOutput(data []byte, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		if len(data) == 0 {
			return "", false
		}
		return "", true
	}
	if len(data) <= maxBytes {
		return string(data), false
	}
	// Back off to a valid UTF-8 rune boundary to avoid splitting
	// a multi-byte character at the truncation point. Only inspect the
	// last few bytes (max 3 for a 4-byte rune) — don't validate the
	// entire slice, as upstream binary data should be preserved as-is.
	end := maxBytes
	for end > 0 && end > maxBytes-utf8.UTFMax {
		if utf8.RuneStart(data[end]) {
			break
		}
		end--
	}
	return string(data[:end]), true
}
