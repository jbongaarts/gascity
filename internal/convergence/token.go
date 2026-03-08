package convergence

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// TokenFile is the filename for the controller token within .gc/.
const TokenFile = "controller.token"

// TokenEnvVar is the environment variable name for the controller token.
const TokenEnvVar = "GC_CONTROLLER_TOKEN"

// GenerateToken generates a cryptographically random 32-byte hex token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating controller token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// WriteToken writes the controller token to .gc/controller.token with mode 0600.
// Uses atomic temp-file + rename to prevent partial reads on crash.
func WriteToken(cityPath, token string) error {
	dir := filepath.Join(cityPath, ".gc")
	path := filepath.Join(dir, TokenFile)
	tmp, err := os.CreateTemp(dir, ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("writing controller token: %w", err)
	}
	tmpName := tmp.Name()
	if _, wErr := tmp.WriteString(token); wErr != nil {
		tmp.Close()        //nolint:errcheck // cleanup after write failure
		os.Remove(tmpName) //nolint:errcheck // cleanup after write failure
		return fmt.Errorf("writing controller token: %w", wErr)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()        //nolint:errcheck // cleanup after chmod failure
		os.Remove(tmpName) //nolint:errcheck // cleanup after chmod failure
		return fmt.Errorf("writing controller token: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()        //nolint:errcheck // cleanup after sync failure
		os.Remove(tmpName) //nolint:errcheck // cleanup after sync failure
		return fmt.Errorf("writing controller token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck // cleanup after close failure
		return fmt.Errorf("writing controller token: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck // cleanup after rename failure
		return fmt.Errorf("writing controller token: %w", err)
	}
	return nil
}

// ReadToken reads the controller token from .gc/controller.token.
func ReadToken(cityPath string) (string, error) {
	path := filepath.Join(cityPath, ".gc", TokenFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading controller token: %w", err)
	}
	return string(data), nil
}

// RemoveToken removes the controller token file.
func RemoveToken(cityPath string) error {
	path := filepath.Join(cityPath, ".gc", TokenFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing controller token: %w", err)
	}
	return nil
}
