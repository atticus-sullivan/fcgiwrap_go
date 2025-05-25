package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
			name:        "missing DOCUMENT_ROOT",
			env:         map[string]string{"SCRIPT_FILENAME": execScript},
			wantErr:     true,
			errContains: "DOCUMENT_ROOT",
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
			cmd, err := prepareCGICommand(tt.env, context.Background())
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
