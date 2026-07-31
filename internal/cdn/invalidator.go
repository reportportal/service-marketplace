package cdn

import "context"

type Invalidator interface {
	Invalidate(ctx context.Context, paths []string) error
}
