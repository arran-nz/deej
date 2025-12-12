package deej

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriharel/deej/pkg/deej/util"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	buildTypeNone    = ""
	buildTypeDev     = "dev"
	buildTypeRelease = "release"

	logDirectory = "logs"
	logFilename  = "deej-latest-run.log"
)

// filterCore wraps a zapcore.Core to filter log entries by logger name.
// This enables the --log-filter flag to show only logs from specific components
// (e.g., "audio-meter", "serial", "process-monitor") for easier debugging.
type filterCore struct {
	zapcore.Core
	filter string
}

// Check implements zapcore.Core. It filters log entries based on whether
// the logger name contains the filter string.
func (f *filterCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if f.filter == "" {
		return f.Core.Check(entry, ce)
	}
	if strings.Contains(entry.LoggerName, f.filter) {
		return f.Core.Check(entry, ce)
	}
	return nil
}

// With implements zapcore.Core. It preserves the filter when creating child loggers.
func (f *filterCore) With(fields []zapcore.Field) zapcore.Core {
	return &filterCore{
		Core:   f.Core.With(fields),
		filter: f.filter,
	}
}

// NewLogger provides a logger instance for the whole program.
func NewLogger(buildType string) (*zap.SugaredLogger, error) {
	return NewLoggerWithFilter(buildType, "")
}

// NewLoggerWithFilter provides a logger with optional name filtering.
// When logFilter is non-empty, only log entries from loggers whose name
// contains the filter string will be output.
func NewLoggerWithFilter(buildType string, logFilter string) (*zap.SugaredLogger, error) {
	var loggerConfig zap.Config

	// release: info and above, log to file only (no UI)
	if buildType == buildTypeRelease {
		if err := util.EnsureDirExists(logDirectory); err != nil {
			return nil, fmt.Errorf("ensure log directory exists: %w", err)
		}

		loggerConfig = zap.NewProductionConfig()
		loggerConfig.OutputPaths = []string{filepath.Join(logDirectory, logFilename)}
		loggerConfig.Encoding = "console"
	} else {
		// development: debug and above, log to stderr only, colorful
		loggerConfig = zap.NewDevelopmentConfig()
		loggerConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	// all build types: make it readable
	loggerConfig.EncoderConfig.EncodeCaller = nil
	loggerConfig.EncoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
	}
	loggerConfig.EncoderConfig.EncodeName = func(s string, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(fmt.Sprintf("%-27s", s))
	}

	logger, err := loggerConfig.Build()
	if err != nil {
		return nil, fmt.Errorf("create zap logger: %w", err)
	}

	// Apply log filter if specified
	if logFilter != "" {
		logger = logger.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
			return &filterCore{Core: c, filter: logFilter}
		}))
	}

	return logger.Sugar(), nil
}
