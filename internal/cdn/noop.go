package cdn

import (
	"context"
	"log"
)

type NoopInvalidator struct{}

func (NoopInvalidator) Invalidate(ctx context.Context, paths []string) error {
	return nil
}

type LogInvalidator struct {
	URLMap string
	Logger *log.Logger
}

func (l *LogInvalidator) Invalidate(ctx context.Context, paths []string) error {
	if l.Logger == nil {
		l.Logger = log.Default()
	}
	if l.URLMap == "" {
		l.Logger.Printf("cdn: invalidation skipped (CDN_URL_MAP not configured), paths=%v", paths)
		return nil
	}
	l.Logger.Printf("cdn: would invalidate urlMap=%s paths=%v", l.URLMap, paths)
	return nil
}
