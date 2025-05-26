// SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/assert"
)

func dummyScript(t *testing.T, root string, name string, exec bool) string {
	t.Helper()
	script := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho OK"), 0644))
	if exec {
		require.NoError(t, os.Chmod(script, 0755))
	}
	return script
}

func TestPrepareCGICommand(t *testing.T) {
	tmpDir := t.TempDir()
	execScript := dummyScript(t, tmpDir, "good.sh", true)
	badScript := dummyScript(t, tmpDir, "bad.sh", false)

	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name:    "missing DOCUMENT_ROOT",
			env:     map[string]string{"SCRIPT_FILENAME": execScript},
			wantErr: false,
		},
		{
			name:        "missing SCRIPT_FILENAME and SCRIPT_NAME",
			env:         map[string]string{"DOCUMENT_ROOT": tmpDir},
			wantErr:     true,
			errContains: "SCRIPT_NAME",
		},
		{
			name:        "SCRIPT_FILENAME not absolute",
			env:         map[string]string{"DOCUMENT_ROOT": tmpDir, "SCRIPT_FILENAME": "foo.sh"},
			wantErr:     true,
			errContains: "absolute",
		},
		{
			name:        "script outside doc root",
			env:         map[string]string{"DOCUMENT_ROOT": tmpDir, "SCRIPT_FILENAME": "/etc/passwd"},
			wantErr:     true,
			errContains: "outside",
		},
		{
			name:        "non-executable script",
			env:         map[string]string{"DOCUMENT_ROOT": tmpDir, "SCRIPT_FILENAME": badScript},
			wantErr:     true,
			errContains: "not executable",
		},
		{
			name:    "valid script",
			env:     map[string]string{"DOCUMENT_ROOT": tmpDir, "SCRIPT_FILENAME": execScript},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := prepareCGICommand(tt.env, make([]string, 0), context.Background())
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, cmd)
				require.True(t, strings.HasSuffix(cmd.Path, execScript))
			}
		})
	}
}

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
