// SPDX-FileCopyrightText: 2025 2025 Lukas Heindl
//
// SPDX-License-Identifier: MIT

package main

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
)

// setup the logging options
func setupLogger(format string, level string) *slog.Logger {
	var handler slog.Handler

	var slevel = slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug": slevel = slog.LevelDebug
	case "info": slevel = slog.LevelInfo
	case "warn": slevel = slog.LevelWarn
	case "error": slevel = slog.LevelError
	}

	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slevel,
		})
	case "text":
		fallthrough
	default:
		handler = tint.NewHandler(os.Stderr, &tint.Options{
			Level:      slevel,
			TimeFormat: time.RFC3339,
			NoColor:    false,
		})
	}

	return slog.New(handler)
}
