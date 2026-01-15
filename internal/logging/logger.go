package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Level represents log severity
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// Logger is a file-based logger for the TTS server
type Logger struct {
	file     *os.File
	logger   *log.Logger
	mu       sync.Mutex
	filePath string
	maxSize  int64
}

var (
	defaultLogger *Logger
	once          sync.Once
)

// Init initializes the default logger
func Init() error {
	var err error
	once.Do(func() {
		defaultLogger, err = newLogger()
	})
	return err
}

func newLogger() (*Logger, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	logDir := filepath.Join(homeDir, ".claude", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	logPath := filepath.Join(logDir, "tts-server.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	multiWriter := io.MultiWriter(file, os.Stderr)

	return &Logger{
		file:     file,
		logger:   log.New(multiWriter, "", 0),
		filePath: logPath,
		maxSize:  10 * 1024 * 1024,
	}, nil
}

func (l *Logger) rotate() error {
	info, err := l.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < l.maxSize {
		return nil
	}

	l.file.Close()
	backupPath := l.filePath + "." + time.Now().Format("2006-01-02-150405")
	os.Rename(l.filePath, backupPath)

	file, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	l.file = file
	l.logger = log.New(io.MultiWriter(file, os.Stderr), "", 0)
	l.cleanupOldLogs()
	return nil
}

func (l *Logger) cleanupOldLogs() {
	dir := filepath.Dir(l.filePath)
	pattern := filepath.Base(l.filePath) + ".*"
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	if len(matches) <= 5 {
		return
	}
	for i := 0; i < len(matches)-5; i++ {
		os.Remove(matches[i])
	}
}

func (l *Logger) log(level Level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.rotate()

	_, file, line, ok := runtime.Caller(2)
	if ok {
		file = filepath.Base(file)
	} else {
		file = "unknown"
		line = 0
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	l.logger.Printf("[%s] %s %s:%d - %s", timestamp, level, file, line, msg)
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
	}
}

func Debug(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(DEBUG, format, args...)
	}
}

func Info(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(INFO, format, args...)
	}
}

func Warn(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(WARN, format, args...)
	}
}

func Error(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(ERROR, format, args...)
	}
}

func Fatal(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(FATAL, format, args...)
		defaultLogger.Close()
	}
	os.Exit(1)
}

func GetLogPath() string {
	if defaultLogger != nil {
		return defaultLogger.filePath
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".claude", "logs", "tts-server.log")
}
