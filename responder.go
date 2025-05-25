package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/fcgi"
	"os"
)

// returns a http handler which handles the cgi request, executes the desired command and passes the response in the http response
func cgiResponder(args arguments) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := fcgi.ProcessEnv(r)

		cmd, err := prepareCGICommand(env, r.Context())
		if err != nil {
			slog.Warn("preparing CGI command failed", "error", err)
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		// wire stdout and stderr
		cmd.Stdout = w
		if args.ForwardErr {
			cmd.Stderr = w
		} else {
			cmd.Stderr = os.Stderr
		}

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

		// proxy body
		if _, err := io.Copy(stdin, r.Body); err != nil {
			slog.Warn("error copying request body", "error", err)
			cmd.Process.Kill()
			stdin.Close()
			return
		}
		stdin.Close()

		if err := cmd.Wait(); err != nil {
			slog.Error("CGI exited with error", "error", err)
		}
	})
}

