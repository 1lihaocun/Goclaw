package longpoll

import (
	"context"
	"fmt"
	"log/slog"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

type slogLarkLogger struct {
	logger *slog.Logger
}

func newSlogLarkLogger(logger *slog.Logger) larkcore.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return &slogLarkLogger{logger: logger}
}

func (l *slogLarkLogger) Debug(_ context.Context, args ...interface{}) {
	l.logger.Debug(formatLarkLog(args...))
}

func (l *slogLarkLogger) Info(_ context.Context, args ...interface{}) {
	l.logger.Info(formatLarkLog(args...))
}

func (l *slogLarkLogger) Warn(_ context.Context, args ...interface{}) {
	l.logger.Warn(formatLarkLog(args...))
}

func (l *slogLarkLogger) Error(_ context.Context, args ...interface{}) {
	l.logger.Error(formatLarkLog(args...))
}

func formatLarkLog(args ...interface{}) string {
	if len(args) == 0 {
		return "feishu sdk"
	}
	return fmt.Sprint(args...)
}
