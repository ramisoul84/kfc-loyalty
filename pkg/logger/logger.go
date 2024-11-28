package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
)

// Logger wraps slog with a custom text handler and helper methods.
type Logger struct {
	*slog.Logger
}

// Config holds logger configuration.
type Config struct {
	Level    string
	Format   string
	Output   string
	FilePath string
	Service  string
}

// New creates a new Logger.
func New(cfg *Config) *Logger {
	if cfg == nil {
		cfg = &Config{Level: "info", Format: "text", Output: "stdout", Service: "kfc-crm"}
	}

	level := parseLevel(cfg.Level)
	writer := createWriter(cfg.Output, cfg.FilePath)

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	} else {
		handler = newTextHandler(writer, level)
	}

	handler = handler.WithAttrs([]slog.Attr{slog.String("service", cfg.Service)})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	return &Logger{Logger: logger}
}

// textHandler is a custom slog.Handler that prints colored, human-readable logs.
type textHandler struct {
	writer io.Writer
	level  slog.Level
	attrs  []slog.Attr
	buf    strings.Builder
}

func newTextHandler(w io.Writer, level slog.Level) *textHandler {
	return &textHandler{
		writer: w,
		level:  level,
		attrs:  []slog.Attr{},
	}
}

func (h *textHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	h.buf.Reset()

	// Time
	h.buf.WriteString(colorGray)
	h.buf.WriteString(r.Time.Format("15:04:05"))
	h.buf.WriteString(colorReset)
	h.buf.WriteString(" ")

	// Level
	switch r.Level {
	case slog.LevelDebug:
		h.buf.WriteString(colorCyan)
		h.buf.WriteString("DBG")
	case slog.LevelInfo:
		h.buf.WriteString(colorGreen)
		h.buf.WriteString("INF")
	case slog.LevelWarn:
		h.buf.WriteString(colorYellow)
		h.buf.WriteString("WRN")
	case slog.LevelError:
		h.buf.WriteString(colorRed)
		h.buf.WriteString("ERR")
	}
	h.buf.WriteString(colorReset)
	h.buf.WriteString(" ")

	// Message
	h.buf.WriteString(r.Message)

	// Handler-level attributes
	for _, attr := range h.attrs {
		if attr.Key != "service" {
			writeAttr(&h.buf, attr)
		}
	}

	// Record-level attributes
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "service" {
			writeAttr(&h.buf, a)
		}
		return true
	})

	h.buf.WriteString("\n")

	_, err := fmt.Fprint(h.writer, h.buf.String())
	return err
}

func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandler := &textHandler{
		writer: h.writer,
		level:  h.level,
	}
	newHandler.attrs = append(h.attrs, attrs...)
	return newHandler
}

func (h *textHandler) WithGroup(_ string) slog.Handler {
	return h
}

// writeAttr writes a single attribute as " key=value" with gray key.
func writeAttr(buf *strings.Builder, attr slog.Attr) {
	buf.WriteString(" ")
	buf.WriteString(colorGray)
	buf.WriteString(attr.Key)
	buf.WriteString("=")
	buf.WriteString(colorReset)
	buf.WriteString(attr.Value.String())
}

// WithRequestID returns a logger with the request_id attribute.
func (l *Logger) WithRequestID(requestID string) *Logger {
	if requestID == "" {
		return l
	}
	return &Logger{Logger: l.Logger.With("request_id", requestID)}
}

// WithError returns a logger with the error attribute.
func (l *Logger) WithError(err error) *Logger {
	if err == nil {
		return l
	}
	return &Logger{Logger: l.Logger.With("error", err.Error())}
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func createWriter(output, filePath string) io.Writer {
	if output == "file" || output == "both" {
		if filePath == "" {
			filePath = "logs/app.log"
		}
		_ = os.MkdirAll("logs", 0755)
		fileWriter := &lumberjack.Logger{
			Filename: filePath,
			MaxSize:  100,
		}
		if output == "both" {
			return io.MultiWriter(os.Stdout, fileWriter)
		}
		return fileWriter
	}
	return os.Stdout
}
