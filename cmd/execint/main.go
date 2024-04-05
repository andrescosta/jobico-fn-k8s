package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrescosta/goico/pkg/runtimes/wasm"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"k8s.io/utils/env"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	event := env.GetString("event", "")
	if event == "" {
		event = "def"
	}
	dir := env.GetString("dir", "")
	if dir == "" {
		panic("no dir")
	}
	wasmFile := env.GetString("py", "")
	if wasmFile == "" {
		panic("no py")
	}
	fmt.Printf("started with event: %s\n", event)
	// Nats
	url := env.GetString("NATS_URL", "nats://queue:4222")
	fmt.Printf("Connecting Nats with %s\n", url)
	nc, err := nats.Connect(url)
	if err != nil {
		panic(err)
	}
	defer nc.Drain()
	js, err := jetstream.New(nc)
	if err != nil {
		panic(err)
	}
	cfg := jetstream.StreamConfig{
		Name:      "EVENTS-" + event,
		Retention: jetstream.WorkQueuePolicy,
		Subjects:  []string{event},
	}

	// JetStream API uses context for timeouts and cancellation.
	stream, err := js.CreateOrUpdateStream(context.Background(), cfg)
	if err != nil {
		panic(err)
	}
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name: "processor-1",
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("created the stream")
	cacheDir := dir + "/cache"

	runtime, err := wasm.NewRuntimeWithCompilationCache(cacheDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing Wazero: %v\n", err)
		os.Exit(1)
	}
	defer runtime.Close(ctx)
	wasmf, err := os.ReadFile(dir + "/python/python.wasm")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading wasm binary: %v\n", err)
		os.Exit(1)
	}
	mounts := []string{
		dir + "/lib/python3.13:/usr/local/lib/python3.13:ro",
		dir + "/sdk:/usr/local/lib/jobico:ro",
		dir + "/prg:/prg",
	}
	args := []string{
		dir + "/python/python.wasm",
		"prg/" + wasmFile,
	}
	buffIn := &bytes.Buffer{}
	buffOut := &bytes.Buffer{}
	buffErr := &bytes.Buffer{}
	e := []wasm.EnvVar{{Key: "PYTHONPATH", Value: "/usr/local/lib/jobico"}}
	mod, err := wasm.NewIntModule(ctx, runtime, wasmf, log, mounts, args, e, buffIn, buffOut, buffErr)
	if err != nil {
		panic(err)
	}
	defer mod.Close(ctx)
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		default:
			fmt.Printf("getting message from %s\n", event)
			msgs, err := cons.Fetch(3)
			if err != nil {
				fmt.Printf("error: %v", err)
			} else {
				for msg := range msgs.Messages() {
					msg.DoubleAck(ctx)
					fmt.Println("executing ....")
					buffErr.Reset()
					buffIn.Reset()
					buffOut.Reset()
					err = mod.Run(ctx)
					if err != nil {
						fmt.Fprintf(os.Stderr, "error instantiating the module: %v\n", err)
						fmt.Printf("Dump Error: %s\n", buffErr.String())
						fmt.Printf("Dump Std out: %s\n", buffOut.String())
						continue
					}
					var res int32
					binary.Read(buffOut, binary.LittleEndian, &res)
					fmt.Printf("Result:\n%d\n", res)
					msgRes, err := io.ReadAll(buffOut)
					if err != nil {
						fmt.Printf("Error reading out buffer: %v\n", err)
						continue
					}
					fmt.Printf("Text:\n%s\n", msgRes)
					var lvl uint8
					var size uint32
					binary.Read(buffErr, binary.LittleEndian, &lvl)
					binary.Read(buffErr, binary.LittleEndian, &size)
					fmt.Printf("Level:%d\n", lvl)
					fmt.Printf("Size:%d\n", size)
					msgLog := make([]byte, size)
					_, err = buffErr.Read(msgLog)
					if err != nil {
						fmt.Printf("Error reading log buffer: %v\n", err)
						continue
					}
					fmt.Printf("Text:%s\n", msgLog)
				}
			}
			fmt.Println("sleeping")
			time.Sleep(1 * time.Second)
		}
	}
}

func log(ctx context.Context, lvl uint32, msg string) error {
	fmt.Printf("%d-%s", lvl, msg)
	return nil
}
