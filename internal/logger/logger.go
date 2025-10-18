package logger

import (
	"fmt"
	"log"
	"os"
)

// Logger provides structured logging with context prefixes
type Logger struct {
	prefix string
	logger *log.Logger
}

// New creates a new root logger
func New() *Logger {
	return &Logger{
		prefix: "",
		logger: log.New(os.Stdout, "", log.LstdFlags),
	}
}

// WithComponent creates a child logger with a component that implements fmt.Stringer
func (l *Logger) WithComponent(component fmt.Stringer) *Logger {
	return &Logger{
		prefix: l.prefix + "[" + component.String() + "]",
		logger: l.logger,
	}
}

// WithPrefix creates a child logger with a custom prefix
func (l *Logger) WithPrefix(prefix string) *Logger {
	return &Logger{
		prefix: l.prefix + "[" + prefix + "]",
		logger: l.logger,
	}
}

// Info logs an informational message
func (l *Logger) Info(msg string, args ...interface{}) {
	l.logger.Printf(l.prefix+" "+msg, args...)
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, args ...interface{}) {
	l.logger.Printf(l.prefix+" [DEBUG] "+msg, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, args ...interface{}) {
	l.logger.Printf(l.prefix+" [WARN] "+msg, args...)
}

// Error logs an error message
func (l *Logger) Error(msg string, args ...interface{}) {
	l.logger.Printf(l.prefix+" [ERROR] "+msg, args...)
}
