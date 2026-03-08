package convergence

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}

	// 32 bytes → 64 hex characters.
	if len(token) != 64 {
		t.Errorf("token length = %d, want 64", len(token))
	}

	// Must be valid hex.
	if _, err := hex.DecodeString(token); err != nil {
		t.Errorf("token is not valid hex: %v", err)
	}

	// Two calls should produce different tokens.
	token2, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == token2 {
		t.Error("two GenerateToken calls returned identical tokens")
	}
}

func TestWriteReadTokenRoundtrip(t *testing.T) {
	dir := t.TempDir()
	gcDir := filepath.Join(dir, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	token := "deadbeef01234567890abcdef01234567890abcdef01234567890abcdef012345"
	if err := WriteToken(dir, token); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	got, err := ReadToken(dir)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != token {
		t.Errorf("ReadToken = %q, want %q", got, token)
	}
}

func TestWriteTokenFileMode(t *testing.T) {
	dir := t.TempDir()
	gcDir := filepath.Join(dir, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteToken(dir, "test-token"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(gcDir, TokenFile))
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}
}

func TestRemoveTokenIdempotent(t *testing.T) {
	dir := t.TempDir()
	gcDir := filepath.Join(dir, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Remove when file doesn't exist — should not error.
	if err := RemoveToken(dir); err != nil {
		t.Errorf("RemoveToken on nonexistent file: %v", err)
	}

	// Write then remove.
	if err := WriteToken(dir, "test-token"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveToken(dir); err != nil {
		t.Errorf("RemoveToken: %v", err)
	}

	// File should be gone.
	path := filepath.Join(gcDir, TokenFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("token file still exists after RemoveToken")
	}

	// Remove again — idempotent.
	if err := RemoveToken(dir); err != nil {
		t.Errorf("second RemoveToken: %v", err)
	}
}

func TestReadTokenMissingFile(t *testing.T) {
	dir := t.TempDir()
	gcDir := filepath.Join(dir, ".gc")
	if err := os.MkdirAll(gcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := ReadToken(dir)
	if err == nil {
		t.Error("ReadToken on missing file should return error")
	}
}
