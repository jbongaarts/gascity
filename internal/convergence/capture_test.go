package convergence

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		maxBytes  int
		wantStr   string
		wantTrunc bool
	}{
		{
			name:      "under limit",
			data:      []byte("hello"),
			maxBytes:  10,
			wantStr:   "hello",
			wantTrunc: false,
		},
		{
			name:      "at limit",
			data:      []byte("hello"),
			maxBytes:  5,
			wantStr:   "hello",
			wantTrunc: false,
		},
		{
			name:      "over limit",
			data:      []byte("hello world"),
			maxBytes:  5,
			wantStr:   "hello",
			wantTrunc: true,
		},
		{
			name:      "empty",
			data:      []byte{},
			maxBytes:  10,
			wantStr:   "",
			wantTrunc: false,
		},
		{
			name:      "nil data",
			data:      nil,
			maxBytes:  10,
			wantStr:   "",
			wantTrunc: false,
		},
		{
			name:      "zero maxBytes with empty data",
			data:      []byte{},
			maxBytes:  0,
			wantStr:   "",
			wantTrunc: false,
		},
		{
			name:      "zero maxBytes with data",
			data:      []byte("hello"),
			maxBytes:  0,
			wantStr:   "",
			wantTrunc: true,
		},
		{
			name:      "large data truncated to MaxOutputBytes",
			data:      []byte(strings.Repeat("x", MaxOutputBytes+100)),
			maxBytes:  MaxOutputBytes,
			wantStr:   strings.Repeat("x", MaxOutputBytes),
			wantTrunc: true,
		},
		{
			name:      "multi-byte UTF-8 not split",
			data:      []byte("hello 世界!"), // 世=3 bytes, 界=3 bytes
			maxBytes:  8,                   // cuts inside 世 if naive
			wantTrunc: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStr, gotTrunc := TruncateOutput(tt.data, tt.maxBytes)
			if tt.wantStr != "" && gotStr != tt.wantStr {
				t.Errorf("TruncateOutput() string = %q (len %d), want %q (len %d)",
					gotStr, len(gotStr), tt.wantStr, len(tt.wantStr))
			}
			if !utf8.ValidString(gotStr) {
				t.Errorf("TruncateOutput() produced invalid UTF-8: %q", gotStr)
			}
			if gotTrunc != tt.wantTrunc {
				t.Errorf("TruncateOutput() truncated = %v, want %v", gotTrunc, tt.wantTrunc)
			}
		})
	}
}
