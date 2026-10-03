//go:build !unix

package deck

import (
	"context"
	"time"
)

func LockAudio(ctx context.Context, path string, wait time.Duration) (func(), error) {
	return func() {}, nil
}
