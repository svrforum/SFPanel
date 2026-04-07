package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func Setup(level string, output io.Writer) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(handler))
}

func SetupFromConfig(logLevel, logFile string) {
	var output io.Writer = os.Stdout
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			slog.Error("failed to open log file, using stdout", "path", logFile, "error", err)
		} else {
			output = io.MultiWriter(os.Stdout, f)
		}
	}
	Setup(logLevel, output)
}
