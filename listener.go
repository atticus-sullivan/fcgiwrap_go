// SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
//
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
)

// generic function to setup a listener. Supports
// - UNIX socket -> second return value is the file which should be deleted in the end
// - TCP Socket
// - nil/stdin
func setupListener(sockArg string) (net.Listener, string, error) {
	var l net.Listener
	var socketPath string

	var err error
	if sockArg == "" {
		slog.Info("using stdin for FastCGI socket")
		l = nil // stdin
	} else if strings.HasPrefix(sockArg, "unix:") {
		path := sockArg[len("unix:"):]
		socketPath = path
		_ = os.Remove(path)
		l, err = net.Listen("unix", path)
		if err != nil {
			return nil, "", fmt.Errorf("listen unix on %v failed with %w", path, err)
		}
		slog.Info("listening on unix socket", "path", path)
	} else if strings.HasPrefix(sockArg, "tcp:") {
		hp := sockArg[len("tcp:"):]
		l, err = net.Listen("tcp", hp)
		if err != nil {
			return nil, "", fmt.Errorf("listen tcp failed on port %v, with %w", hp, err)
		}
		slog.Info("listening on tcp socket", "hostport", hp)
	} else {
		return nil, "", fmt.Errorf("invalid socket URL '%v'", sockArg)
	}

	return l, socketPath, nil
}
