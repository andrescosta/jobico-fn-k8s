package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrescosta/goico/pkg/runtimes/wasm"
)

type scriptType int8

const (
	ScriptPython scriptType = iota
	ScriptJavascript
)

type genericModuleRunner struct {
	mod    *wasm.GenericModule
	types  scriptType
	script string
	dir    string
	exec   string
	pool   *sync.Pool
	log    wasm.LogFn
}

type errorRun struct {
	err    error
	stdOut []byte
	stdErr []byte
}

func (errorRun) Error() string {
	return ""
}

func NewGenericModuleRunner(ctx context.Context, script string, service Service, log wasm.LogFn) (moduleRunner, error) {
	var scriptType scriptType
	var exec string
	if strings.HasSuffix(script, "py") {
		scriptType = ScriptPython
		exec = "/python/python.wasm"
	} else {
		if strings.HasSuffix(script, "js") {
			scriptType = ScriptJavascript
			exec = "/js/js.wasm"
		} else {
			return nil, errors.New("")
		}
	}
	wasmp := path.Join(service.dir, exec)
	wasmf, err := os.ReadFile(wasmp)
	if err != nil {
		return nil, err
	}
	cacheDir := path.Join(service.dir, "/.cache")
	mod, err := wasm.NewGenericModule(ctx, cacheDir, wasmf, log)
	if err != nil {
		return nil, err
	}

	return &genericModuleRunner{
		mod:    mod,
		types:  scriptType,
		script: script,
		dir:    service.dir,
		exec:   exec,
		pool: &sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}, nil
}

func (g *genericModuleRunner) close(ctx context.Context) error {
	return g.mod.Close(ctx)
}

func (g *genericModuleRunner) execute(ctx context.Context, msg []byte) (uint64, string, error) {
	buffIn := g.pool.Get().(*bytes.Buffer)
	buffIn.Reset()
	defer g.pool.Put(buffIn)
	buffOut := g.pool.Get().(*bytes.Buffer)
	buffOut.Reset()
	defer g.pool.Put(buffOut)
	buffErr := g.pool.Get().(*bytes.Buffer)
	buffErr.Reset()
	defer g.pool.Put(buffErr)
	_, err := buffIn.Write(msg)
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	switch g.types {
	case ScriptJavascript:
		if err := g.runJavascript(ctx, buffIn, buffOut, buffErr); err != nil {
			return 0, "", errorRun{
				err:    err,
				stdOut: buffOut.Bytes(),
				stdErr: buffErr.Bytes(),
			}
		}
		return g.processJavascriptOutput(ctx, buffOut, buffErr)
	case ScriptPython:
		if err := g.runPython(ctx, buffIn, buffOut, buffErr); err != nil {
			return 0, "", errorRun{
				err:    err,
				stdOut: buffOut.Bytes(),
				stdErr: buffErr.Bytes(),
			}
		}
		return g.processPythonOutput(ctx, buffOut, buffErr)
	}
	return 0, "", errors.New("unsupported")
}

func (g *genericModuleRunner) processPythonOutput(ctx context.Context, buffOut *bytes.Buffer, buffErr *bytes.Buffer) (uint64, string, error) {
	var res int32
	binary.Read(buffOut, binary.LittleEndian, &res)
	msgRes, err := io.ReadAll(buffOut)
	if err != nil {
		return 0, "", err
	}
	var lvl uint8
	var size uint32
	binary.Read(buffErr, binary.LittleEndian, &lvl)
	binary.Read(buffErr, binary.LittleEndian, &size)
	msgLog := make([]byte, size)
	_, err = buffErr.Read(msgLog)
	if err != nil {
		return 0, "", err
	}
	g.log(ctx, uint32(lvl), string(msgLog))
	return uint64(res), string(msgRes), err
}

func (g *genericModuleRunner) processJavascriptOutput(ctx context.Context, buffOut *bytes.Buffer, buffErr *bytes.Buffer) (uint64, string, error) {
	scErr := bufio.NewScanner(buffErr)
	for scErr.Scan() {
		msgErr := scErr.Text()
		lvl, err := strconv.Atoi(string(msgErr[0]))
		if err != nil {
			lvl = 0
		}
		if len(msgErr) > 1 {
			g.log(ctx, uint32(lvl), msgErr[1:])
		} else {
			g.log(ctx, uint32(lvl), "!!!NO LOG!!!")
		}
	}
	scOut := bufio.NewScanner(buffOut)
	if scOut.Scan() {
		msgOut := scOut.Text()
		if len(msgOut) > 10 {
			res, err := strconv.Atoi(strings.TrimSpace(msgOut[0:11]))
			if err != nil {
				res = -1
			}
			msg := msgOut[11:]
			return uint64(res), msg, nil
		}
	}
	return 0, "!! NO RESULT !!", nil
}

func (g *genericModuleRunner) runPython(ctx context.Context, buffIn *bytes.Buffer, buffOut *bytes.Buffer, buffErr *bytes.Buffer) error {
	mounts := []string{
		path.Join(g.dir, "python/lib/python3.13") + ":/usr/local/lib/python3.13:ro",
		path.Join(g.dir, "python/sdk") + ":/usr/local/lib/jobico:ro",
		path.Join(g.dir, "python/prg") + ":/prg",
	}
	args := []string{
		g.exec,
		"/prg/" + g.script,
	}
	e := []wasm.EnvVar{{Key: "PYTHONPATH", Value: "/usr/local/lib/jobico"}}
	return g.mod.Run(ctx, mounts, args, e, buffIn, buffOut, buffErr)
}

func (g *genericModuleRunner) runJavascript(ctx context.Context, buffIn *bytes.Buffer, buffOut *bytes.Buffer, buffErr *bytes.Buffer) error {
	mounts := []string{
		path.Join(g.dir, "/js") + ":/js",
	}
	args := []string{
		g.exec,
		"--module=/js/prg/" + g.script,
	}
	fmt.Printf("%v\n", args)
	e := []wasm.EnvVar{}
	return g.mod.Run(ctx, mounts, args, e, buffIn, buffOut, buffErr)
}
