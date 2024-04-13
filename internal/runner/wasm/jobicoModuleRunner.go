package wasm

import (
	"context"
	"os"
	"path"
	"time"

	runtime "github.com/andrescosta/goico/pkg/runtimes/wasm"
)

type jobicoletModuleRunner struct {
	mod *runtime.JobicoletModule
}

func NewjobicoletModuleRunner(ctx context.Context, dir, event, wasmFile string, log runtime.LogFn) (*jobicoletModuleRunner, error) {
	cacheDir := path.Join(dir, ".cache")
	file := path.Join(dir, "wasm", wasmFile)
	wasmbytes, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	wasmModule, err := runtime.NewJobicoletModule(ctx, cacheDir, wasmbytes, "event", log)
	if err != nil {
		return nil, err
	}
	return &jobicoletModuleRunner{
		mod: wasmModule,
	}, nil
}

func (j *jobicoletModuleRunner) Run(ctx context.Context, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, _, err := j.mod.Run(ctx, string(msg))
	return err
}

func (j *jobicoletModuleRunner) Close(ctx context.Context) error {
	return j.mod.Close(ctx)
}
