package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// validateScript ensures the requested script path is under docRoot and is executable
func validateScript(script string, docRoot string) error {
	if !filepath.IsAbs(script) {
		return fmt.Errorf("script path must be absolute: %s", script)
	}

	// Clean up the path (removes "."/".." components)
	script = filepath.Clean(script)

	if docRoot != "" {
		// Ensure path is under docRoot
		rel, err := filepath.Rel(docRoot, script)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("script path (%s) outside DOCUMENT_ROOT (%s)", script, docRoot)
		}
	}

	// Lstat file (does not follow symlink) to ensure target is a regular executable and no symlink
	// symlink are the root of many vulnerabilities!
	info, err := os.Lstat(script)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("script not found: %w", err)
		}
		return fmt.Errorf("failed to lstat script: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Symlinks are unsupported %s", script)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("script is not a regular file: %s", script)
	}
	if info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("script not executable: %s", script)
	}

	slog.Debug("script validated", "script", script)
	return nil
}

// prepareCGICommand constructs an *exec.Cmd from the cgi request
func prepareCGICommand(env map[string]string, ctx context.Context) (*exec.Cmd, error) {
	script := env["SCRIPT_FILENAME"]

	docRoot, ok := env["DOCUMENT_ROOT"]
	if script == "" && (!ok || docRoot == "") {
		return nil, fmt.Errorf("DOCUMENT_ROOT not defined but needs to be")
	}

	scriptName, ok := env["SCRIPT_NAME"]
	if script == "" && (!ok || scriptName == "") {
		return nil, fmt.Errorf("SCRIPT_NAME not defined but needs to be")
	}
	if script == "" {
		script = filepath.Join(docRoot, scriptName)
	}

	if err := validateScript(script, docRoot); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	return cmd, nil
}
