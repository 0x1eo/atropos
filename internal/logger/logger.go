package logger

import (
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	log  *zap.Logger
	once sync.Once
)

func Get() *zap.Logger {
	once.Do(func() {
		log = newLogger()
	})
	return log
}

func newLogger() *zap.Logger {
	config := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "severity",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "directive",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(config),
		zapcore.AddSync(os.Stdout),
		zap.NewAtomicLevelAt(zap.InfoLevel),
	)

	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
}

func CutInitiated(target, action string, entropy float64) {
	Get().Info("CUT_INITIATED",
		zap.String("target", target),
		zap.String("action", action),
		zap.Float64("entropy", entropy),
	)
}

func CutExecuted(target, action string, latencyMs int64) {
	Get().Info("CUT_EXECUTED",
		zap.String("target", target),
		zap.String("action", action),
		zap.String("status", "SUCCESS"),
		zap.Int64("latency_ms", latencyMs),
	)
}

func CutFailed(target, action string, err error) {
	Get().Error("CUT_FAILED",
		zap.String("target", target),
		zap.String("action", action),
		zap.String("status", "FAILURE"),
		zap.Error(err),
	)
}

func Escalation(target, fromAction, toAction string, reason string) {
	Get().Warn("ESCALATION",
		zap.String("target", target),
		zap.String("from_action", fromAction),
		zap.String("to_action", toAction),
		zap.String("reason", reason),
	)
}

func WebhookReceived(node string, entropy float64, valid bool) {
	Get().Info("WEBHOOK_RECEIVED",
		zap.String("node", node),
		zap.Float64("entropy", entropy),
		zap.Bool("signature_valid", valid),
	)
}
