package main

import (
	"context"
	"fmt"
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
	wasmFile := env.GetString("wasm", "")
	if wasmFile == "" {
		panic("no wasm")
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
	wasmRuntime, err := wasm.NewRuntimeWithCompilationCache(cacheDir)
	if err != nil {
		panic(err)
	}
	defer func() { _ = wasmRuntime.Close(context.Background()) }()

	wasmbytes, err := os.ReadFile(dir + "/wasm/" + wasmFile)
	if err != nil {
		panic(err)
	}

	wasmModule, err := wasm.NewModule(ctx, wasmRuntime, wasmbytes, "event", log)
	if err != nil {
		panic(err)
	}
	defer func() { _ = wasmModule.Close(context.Background()) }()

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
					c, r, err := wasmModule.Run(context.Background(), string(msg.Data()))
					if err != nil {
						fmt.Printf("error: %v", err)
						continue
					}
					fmt.Printf("%d-%s", c, r)
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
