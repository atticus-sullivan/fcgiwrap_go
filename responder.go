// SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
//
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"io"
	"log/slog"
	"net/http"
	"net/http/fcgi"
	"os"
	"strings"
)

// returns a http handler which handles the cgi request, executes the desired command and passes the response in the http response
func cgiResponder(args arguments, inherited_env []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := fcgi.ProcessEnv(r)

		cmd, err := prepareCGICommand(env, inherited_env, r.Context())
		if err != nil {
			slog.Warn("preparing CGI command failed", "error", err)
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		// wire stdout
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			slog.Warn("failed to pipe stdout", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// wire stderr
		if args.ForwardErr {
			cmd.Stderr = w
		} else {
			cmd.Stderr = os.Stderr
		}

		// wire stdin
		stdin, err := cmd.StdinPipe()
		if err != nil {
			slog.Warn("failed to prepare command", "error", err)
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		if err := cmd.Start(); err != nil {
			slog.Error("failed to start CGI", "error", err)
			http.Error(w, "failed to start CGI: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer slog.Debug("CGI process finished", "pid", cmd.Process.Pid)

		// Copy request body to CGI stdin
		go func() {
			io.Copy(stdin, r.Body)
			stdin.Close()
		}()

		// Use bufio to scan headers
		br := bufio.NewReader(stdout)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				slog.Warn("error reading CGI headers", "error", err)
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
				return
			}

			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break // end of headers
			}

			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				w.Header().Add(key, val)
			}
		}

		// Stream the remaining body
		if _, err := io.Copy(w, br); err != nil {
			slog.Warn("error copying CGI body", "error", err)
		}

		if err := cmd.Wait(); err != nil {
			slog.Error("CGI exited with error", "error", err)
		}
	})
}
