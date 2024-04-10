package service

import (
	"context"
	"os"
	"path"
	"time"

	"github.com/andrescosta/goico/pkg/runtimes/wasm"
)

type jobicoletModuleRunner struct {
	mod *wasm.JobicoletModule
}

func NewjobicoletModuleRunner(ctx context.Context, wasmFile string, service Service, log wasm.LogFn) (moduleRunner, error) {
	cacheDir := path.Join(service.dir, ".cache")
	file := path.Join(service.dir, "wasm", wasmFile)
	wasmbytes, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	wasmModule, err := wasm.NewJobicoletModule(ctx, cacheDir, wasmbytes, "event", log)
	if err != nil {
		return nil, err
	}
	return &jobicoletModuleRunner{
		mod: wasmModule,
	}, nil
}

func (j *jobicoletModuleRunner) execute(ctx context.Context, msg []byte) (uint64, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return j.mod.Run(ctx, string(msg))
}

func (j *jobicoletModuleRunner) close(ctx context.Context) error {
	return j.mod.Close(ctx)
}
