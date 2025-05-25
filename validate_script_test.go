package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateScript_BasicCases(t *testing.T) {
	tmpDir := t.TempDir()

	// Create valid script
	scriptPath := filepath.Join(tmpDir, "ok.sh")
	assert.NoError(t, os.WriteFile(scriptPath, []byte("echo ok"), 0o755))

	t.Run("Valid absolute executable script", func(t *testing.T) {
		assert.NoError(t, validateScript(scriptPath, tmpDir))
	})

	t.Run("Relative path should fail", func(t *testing.T) {
		err := validateScript("rel/test.sh", tmpDir)
		assert.ErrorContains(t, err, "absolute")
	})

	t.Run("Script outside DOCUMENT_ROOT", func(t *testing.T) {
		outside := filepath.Join(os.TempDir(), "evil.sh")
		_ = os.WriteFile(outside, []byte("echo bad"), 0o755)
		err := validateScript(outside, tmpDir)
		assert.ErrorContains(t, err, "outside")
	})

	t.Run("Non-existent file", func(t *testing.T) {
		missing := filepath.Join(tmpDir, "nofile")
		err := validateScript(missing, tmpDir)
		assert.ErrorContains(t, err, "script not found")
	})

	t.Run("Non-executable script", func(t *testing.T) {
		nonExec := filepath.Join(tmpDir, "noexec.sh")
		_ = os.WriteFile(nonExec, []byte("echo x"), 0o644)
		err := validateScript(nonExec, tmpDir)
		assert.ErrorContains(t, err, "not executable")
	})

	t.Run("Path is directory", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "dir")
		_ = os.Mkdir(dir, 0o755)
		err := validateScript(dir, tmpDir)
		assert.ErrorContains(t, err, "not a regular file")
	})

	t.Run("Path with .. normalized correctly", func(t *testing.T) {
		norm := filepath.Join(tmpDir, "subdir", "..", "ok.sh")
		assert.NoError(t, validateScript(norm, tmpDir))
	})
}

func TestValidateScript_Symlinks(t *testing.T) {
	tmpDir := t.TempDir()

	realScript := filepath.Join(tmpDir, "real.sh")
	assert.NoError(t, os.WriteFile(realScript, []byte("echo real"), 0o755))

	t.Run("Reject symlink to valid file", func(t *testing.T) {
		link := filepath.Join(tmpDir, "link.sh")
		assert.NoError(t, os.Symlink(realScript, link))
		assert.ErrorContains(t, validateScript(link, tmpDir), "Symlinks are unsupported")
	})
}
