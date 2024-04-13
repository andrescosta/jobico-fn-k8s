package runner

import "context"

type ModuleRunner interface {
	Run(context.Context, []byte) error
	Close(ctx context.Context) error
}
