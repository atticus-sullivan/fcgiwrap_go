package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/lmittmann/tint"
	"golang.org/x/sync/semaphore"
)

// arguments holds command-line arguments parsed by go-arg
type arguments struct {
	Socket     string `arg:"-s,--socket" help:"Socket URL (tcp:host:port or unix:/path). Default: stdin"`
	Timeout    int    `arg:"-t,--timeout" help:"Idle timeout in seconds; exit if no new request within this period"`
	Workers    int    `arg:"-w,--workers" help:"Max concurrent CGI handlers (default 1)"`
	ForwardErr bool   `arg:"-f,--forward-stderr" help:"Forward CGI stderr over FastCGI instead of host stderr"`
}

// parse the arguments with go-arg. Uses MustParese -> might fail/panic
func parseArgs() arguments {
	args := arguments{
		Workers: 1,
	}
	arg.MustParse(&args)
	return args
}

// setup the logging options
func setupLogger() *slog.Logger {
	return slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:      slog.LevelInfo,
		TimeFormat: time.RFC3339,
		NoColor:    false,
	}))
}

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

// validateScript ensures the requested script path is under docRoot and is executable
func validateScript(script string, docRoot string) error {
	if !filepath.IsAbs(script) {
		return fmt.Errorf("script path must be absolute: %s", script)
	}

	// Clean up the path (removes "."/".." components)
	script = filepath.Clean(script)

	// Ensure path is under docRoot
	rel, err := filepath.Rel(docRoot, script)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("script path (%s) outside DOCUMENT_ROOT (%s)", script, docRoot)
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

// fcgiHandler wraps handler to enforce limits and track active handlers
func fcgiHandler(active *sync.WaitGroup, sem *semaphore.Weighted, refreshTimer func(), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// track active
		active.Add(1)
		defer active.Done()

		refreshTimer()

		slog.Debug("waiting for worker slot")
		if sem != nil {
			if err := sem.Acquire(r.Context(), 1); err != nil {
				slog.Error("Failed waiting for worker slot", "err", err)
				return
			}
			defer func() {
				sem.Release(1)
			}()
		}

		next.ServeHTTP(w, r)
	})
}

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

func main() {
	args := parseArgs()

	slog.SetDefault(setupLogger())

	l, sockPath, err := setupListener(args.Socket)
	if err != nil {
		slog.Error("Initializing listener failed", "err", err)
		panic(err)
	}

	var timer *time.Timer
	var timerCh <-chan time.Time
	var timerReset func()
	if args.Timeout > 0 {
		timer = time.NewTimer(time.Duration(args.Timeout) * time.Second)
		timerCh = timer.C
		timerReset = func() {
			timer.Reset(time.Duration(args.Timeout) * time.Second)
		}
	} else {
		timerCh = make(chan time.Time) // never fires
		timerReset = func() {}
	}

	wg := &sync.WaitGroup{}
	var sem *semaphore.Weighted
	if args.Workers > 0 {
		sem = semaphore.NewWeighted(int64(args.Workers))
	}

	h := fcgiHandler(wg, sem, timerReset, cgiResponder(args))
	errCh := make(chan error, 1)
	go func() {
		errCh <- fcgi.Serve(l, h)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, fcgi.ErrConnClosed) {
			slog.Error("fcgi.Serve error", "error", err)
		}
	case <-sigCh:
		slog.Info("shutdown signal received, waiting for active handlers")
	case <-timerCh:
		slog.Info("timeout reached")
	}

	// terminate / cleanup
	if l != nil {
		// this should also make the serve function/goroutine terminate
		// TODO how to when l is nil?
		l.Close()
	}

	c := make(chan struct{})
	go func() { wg.Wait(); close(c) }()
	select {
	case <-c:
		slog.Info("all handlers completed")
	case <-time.After(30 * time.Second):
		slog.Warn("timeout waiting for handlers to finish")
	}

	if sockPath != "" {
		_ = os.Remove(sockPath)
		slog.Debug("removed unix socket", "path", sockPath)
	}

	os.Exit(0) // should terminate/kill all remaining goroutines (particularly the serve goroutine if l=nil)
}
