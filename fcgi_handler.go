package main

import (
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/semaphore"
)

// fcgiHandler wraps handler to enforce limits and track active handlers
func fcgiHandler(activeJobs *atomic.Int32, wg *sync.WaitGroup, sem *semaphore.Weighted, refreshTimer func(), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// track active
		wg.Add(1)
		defer wg.Done()
		activeJobs.Add(1)
		defer activeJobs.Add(-1)

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

		// refresh the timer AFTER accepting a new job
		refreshTimer()

		next.ServeHTTP(w, r)

		// refresh the timer after finishing the job
		refreshTimer()
	})
}

