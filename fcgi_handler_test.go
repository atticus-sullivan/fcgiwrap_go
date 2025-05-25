// SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
//
// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/semaphore"
)

func TestFCGIHandlerConcurrencyLimit(t *testing.T) {
	var active atomic.Int32
	var current, max int32
	sem := semaphore.NewWeighted(2)
	wg := &sync.WaitGroup{}

	handler := fcgiHandler(&active, wg, sem, func() {}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&current, 1)
		defer atomic.AddInt32(&current, -1)

		for {
			old := atomic.LoadInt32(&max)
			if cur <= old || atomic.CompareAndSwapInt32(&max, old, cur) {
				break
			}
		}

		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	var testWg sync.WaitGroup
	for range 5 {
		testWg.Add(1)
		go func() {
			defer testWg.Done()
			handler.ServeHTTP(w, r)
		}()
	}

	testWg.Wait()
	assert.LessOrEqual(t, max, int32(2), "Exceeded worker limit")
}
