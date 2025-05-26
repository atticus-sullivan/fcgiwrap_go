// SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
//
// SPDX-License-Identifier: MIT

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
func prepareCGICommand(env map[string]string, inherited_env []string, ctx context.Context) (*exec.Cmd, error) {
	script := env["SCRIPT_FILENAME"]

	docRoot, ok := env["DOCUMENT_ROOT"]
	if script == "" && (!ok || docRoot == "") {
		return nil, fmt.Errorf("DOCUMENT_ROOT not defined but needs to be")
	}

	if script == "" {
		scriptName, ok := env["SCRIPT_NAME"]
		if !ok || scriptName == "" {
			return nil, fmt.Errorf("SCRIPT_NAME not defined but needs to be")
		}
		script = filepath.Join(docRoot, scriptName)
	}

	if err := validateScript(script, docRoot); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, script)
	cmd.Env = inherit_environment(env, inherited_env)

	if dir, ok := env["FCGI_CHDIR"]; ok {
		switch dir {
		case "-":
		// explicit "-": skip chdir altogether
		default:
			// ensure it's absolute for clarity
			if !filepath.IsAbs(dir) {
				return nil, fmt.Errorf("FCGI_CHDIR must be absolute: %q", dir)
			}
			// stat it
			info, err := os.Stat(dir)
			if err != nil {
				return nil, fmt.Errorf("FCGI_CHDIR stat failed: %w", err)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("FCGI_CHDIR is not a directory: %q", dir)
			}
			// at this point we know chdir will succeed
			cmd.Dir = dir
		}
	} else {
		// fallback to script’s directory
		cmd.Dir = filepath.Dir(script)
	}

	return cmd, nil
}

func inherit_environment(env map[string]string, inherited_env []string) []string {
	ret_env := make([]string, 0, len(env)+len(inherited_env))
	seen := make(map[string]bool)

	for k,v := range env {
		if _, ok := seen[k]; ok {
			continue
		}
		ret_env = append(ret_env, k+"="+v)
		seen[k] = true
	}

	for _, i := range inherited_env {
		tmp := strings.SplitN(i, "=", 2)
		k,_ := tmp[0], tmp[1]
		if _, ok := seen[k]; ok {
			continue
		}
		ret_env = append(ret_env, i)
		seen[k] = true
	}

	return ret_env
}
