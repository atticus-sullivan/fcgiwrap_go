// SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
//
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"log/slog"
	"net/http/fcgi"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	"golang.org/x/sync/semaphore"
)

// arguments holds command-line arguments parsed by go-arg
type arguments struct {
	Socket     string `arg:"-s,--socket" help:"Socket URL (tcp:host:port or unix:/path). Default: stdin"`
	Timeout    int    `arg:"-t,--timeout" help:"Idle timeout in seconds; exit if no new request within this period"`
	Workers    int    `arg:"-w,--workers" help:"Max concurrent CGI handlers (default 1)"`
	ForwardErr bool   `arg:"-f,--forward-stderr" help:"Forward CGI stderr over FastCGI instead of host stderr"`
	LogFormat  string `arg:"--log-format" help:"Log format: 'json' (default) or 'test'"`
}

// parse the arguments with go-arg. Uses MustParese -> might fail/panic
func parseArgs() arguments {
	args := arguments{
		Workers:   1,
		LogFormat: "json",
	}
	arg.MustParse(&args)
	return args
}

func main() {
	args := parseArgs()
	slog.SetDefault(setupLogger(args.LogFormat))
	slog.Info("starting fcgiwrap-go", "workers", args.Workers, "timeout", args.Timeout, "socket", args.Socket)

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

	var activeJobs atomic.Int32
	var wg sync.WaitGroup
	var sem *semaphore.Weighted
	if args.Workers > 0 {
		sem = semaphore.NewWeighted(int64(args.Workers))
	}

	h := fcgiHandler(&activeJobs, &wg, sem, timerReset, cgiResponder(args))
	errCh := make(chan error, 1)
	go func() {
		errCh <- fcgi.Serve(l, h)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

loop:
	for {
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, fcgi.ErrConnClosed) {
				slog.Error("fcgi.Serve error", "error", err)
			}
			break loop
		case <-sigCh:
			slog.Info("shutdown signal received, waiting for active handlers")
			break loop
		case <-timerCh:
			if activeJobs.Load() == 0 {
				slog.Info("timeout reached and no active jobs")
				break loop
			} else {
				slog.Debug("timeout fired but there are still active jobs — resetting timer")
				timerReset()
			}
		}
	}

	// terminate / cleanup
	if l != nil {
		// this should also make the serve function/goroutine terminate
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
