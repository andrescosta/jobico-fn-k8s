package runner

import "context"

type moduleRunner interface {
	run(context.Context, []byte) (uint64, string, error)
	close(ctx context.Context) error
}
