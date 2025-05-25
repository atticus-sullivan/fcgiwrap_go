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

// Args holds command-line arguments parsed by go-arg
type arguments struct {
	Socket     string `arg:"-s,--socket" help:"Socket URL (tcp:host:port or unix:/path). Default: stdin"`
	Timeout    int    `arg:"-t,--timeout" help:"Idle timeout in seconds; exit if no new request within this period"`
	Workers    int    `arg:"-w,--workers" help:"Max concurrent CGI handlers (default 1)"`
	ForwardErr bool   `arg:"-f,--forward-stderr" help:"Forward CGI stderr over FastCGI instead of host stderr"`
}

func parse_args() arguments {
	args := arguments{
		Workers: 1,
	}
	arg.MustParse(&args)
	return args
}

func setup_logger() *slog.Logger {
	return slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:      slog.LevelInfo,
		TimeFormat: time.RFC3339,
		NoColor:    false,
	}))
}

func setup_listener(sock_arg string) (net.Listener, string, error) {
	var l net.Listener
	var socket_path string

	var err error
	if sock_arg == "" {
		slog.Info("using stdin for FastCGI socket")
		l = nil // stdin
	} else if strings.HasPrefix(sock_arg, "unix:") {
		path := sock_arg[len("unix:"):]
		socket_path = path
		_ = os.Remove(path)
		l, err = net.Listen("unix", path)
		if err != nil {
			return nil, "", fmt.Errorf("listen unix on %v failed with %w", path, err)
		}
		slog.Info("listening on unix socket", "path", path)
	} else if strings.HasPrefix(sock_arg, "tcp:") {
		hp := sock_arg[len("tcp:"):]
		l, err = net.Listen("tcp", hp)
		if err != nil {
			return nil, "", fmt.Errorf("listen tcp failed on port %v, with %w", hp, err)
		}
		slog.Info("listening on tcp socket", "hostport", hp)
	} else {
		return nil, "", fmt.Errorf("invalid socket URL '%v'", sock_arg)
	}
	
	return l, socket_path, nil
}


// validateScript ensures the requested script path is under docRoot and is executable
func validateScript(script, docRoot string) error {
	// SCRIPT_FILENAME must be absolute
	if !filepath.IsAbs(script) {
		return fmt.Errorf("SCRIPT_FILENAME must be absolute: %s", script)
	}

	// Clean up the path (removes "."/".." components)
	clean := filepath.Clean(script)

	// Ensure clean path is under docRoot
	rel, err := filepath.Rel(docRoot, clean)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("SCRIPT_FILENAME outside DOCUMENT_ROOT (%s): %s", docRoot, clean)
	}

	// Lstat file (does not follow symlink) to ensure target is a regular executable and no symlink
	// symlink are the root of many vulnerabilities!
	info, err := os.Lstat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("script not found: %w", err)
		}
		return fmt.Errorf("failed to stat script: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Symlinks are unsupported %s", clean)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("script is not a regular file: %s", clean)
	}
	if info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("script not executable: %s", clean)
	}

	slog.Debug("script validated", "script", clean)
	return nil
}

// prepareCGICommand constructs an *exec.Cmd from the cgi request
func prepareCGICommand(env map[string]string, ctx context.Context, forwardErr bool) (*exec.Cmd, error) {
	script := env["SCRIPT_FILENAME"]

	docRoot, ok := env["DOCUMENT_ROOT"]
	if script == "" && (!ok || docRoot == "") {
		return nil, fmt.Errorf("DOCUMENT_ROOT not defined but needs to be")
	}

	script_name, ok := env["SCRIPT_NAME"]
	if script == "" && (!ok || script_name == "") {
		return nil, fmt.Errorf("SCRIPT_NAME not defined but needs to be")
	}
	if script == "" {
		script = filepath.Join(docRoot, script_name)
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

// fcgiHandler wraps handler to enforce limits and track active
func fcgiHandler(active *sync.WaitGroup, sem *semaphore.Weighted, refresh_timer func(), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// track active
		active.Add(1)
		defer active.Done()

		refresh_timer()

		slog.Debug("waiting for worker slot")
		if sem != nil {
			if err := sem.Acquire(context.TODO(), 1); err != nil {
				slog.Error("Failed waiting for worker slot", "err", err)
				return
			}
			defer func(){
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

		cmd, err := prepareCGICommand(env, r.Context(), args.ForwardErr)
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
	slog.SetDefault(setup_logger())

	args := parse_args()
	l, sock_path, err := setup_listener(args.Socket)
	if err != nil {
		slog.Error("Initializing listener failed", "err", err)
		panic(err)
	}

	var timer *time.Timer
	var timer_ch <-chan time.Time
	var timer_reset func()
	if args.Timeout > 0 {
		timer = time.NewTimer(time.Duration(args.Timeout) * time.Second)
		timer_ch = timer.C
		timer_reset = func(){
			// TODO reed docs
			timer.Reset(time.Duration(args.Timeout) * time.Second)
		}
	} else {
		timer_ch = make(chan time.Time) // never fires
		timer_reset = func(){}
	}

	wg := &sync.WaitGroup{}
	var sem *semaphore.Weighted
	if args.Workers > 0 {
		sem = semaphore.NewWeighted(int64(args.Workers))
	}

	h := fcgiHandler(wg, sem, timer_reset, cgiResponder(args))
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
	case <-timer_ch:
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
	case <-time.After(30*time.Second):
		slog.Warn("timeout waiting for handlers to finish")
	}

	if sock_path != "" {
		_ = os.Remove(sock_path)
		slog.Debug("removed unix socket", "path", sock_path)
	}
}
