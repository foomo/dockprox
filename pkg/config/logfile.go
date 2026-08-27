package config

import (
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/pkg/errors"
	"gopkg.in/natefinch/lumberjack.v2"
)

// LogFileName is the log filename written under the OS cache directory.
const LogFileName = "dockprox.log"

const (
	logMaxSizeMB  = 5
	logMaxBackups = 3
)

// DefaultLogPath returns the OS-appropriate cache path for dockprox's log
// file (e.g. ~/Library/Caches/dockprox/dockprox.log on macOS,
// ~/.cache/dockprox/dockprox.log on Linux).
func DefaultLogPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", errors.Wrap(err, "user cache dir")
	}

	return filepath.Join(dir, "org.foomo.dockprox", LogFileName), nil
}

// ResolveLogPath returns cfg.LogFile if set, otherwise DefaultLogPath.
func ResolveLogPath(cfg *Config) (string, error) {
	if cfg.LogFile != "" {
		return cfg.LogFile, nil
	}

	return DefaultLogPath()
}

// OpenLogWriter resolves the log file path via ResolveLogPath, creates its
// parent directory and the file itself (lumberjack only creates it lazily
// on first write, but "reveal in Finder" needs it to exist beforehand), and
// returns a size-rotated writer for it.
func OpenLogWriter(cfg *Config) (io.Writer, error) {
	path, err := ResolveLogPath(cfg)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, errors.Wrap(err, "mkdir log parent")
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, errors.Wrap(err, "create log file")
	}

	if err := f.Close(); err != nil {
		return nil, errors.Wrap(err, "create log file")
	}

	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    logMaxSizeMB,
		MaxBackups: logMaxBackups,
	}, nil
}

// LevelFromString maps a Config.LogLevel value to a charmbracelet/log
// Level, defaulting to InfoLevel for "" or any unrecognized value.
func LevelFromString(s string) log.Level {
	switch s {
	case "debug":
		return log.DebugLevel
	case "warn":
		return log.WarnLevel
	case "error":
		return log.ErrorLevel
	default:
		return log.InfoLevel
	}
}
