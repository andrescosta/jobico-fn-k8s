package service

import "context"

type moduleRunner interface {
	execute(context.Context, []byte) (uint64, string, error)
	close(ctx context.Context) error
}
