package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/andrescosta/goico/pkg/runtimes/wasm"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"k8s.io/utils/env"
)

type scriptType int8

const (
	ScriptPython scriptType = iota
	ScriptJavascript
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	event := env.GetString("event", "")
	if event == "" {
		event = "def"
	}
	event = strings.TrimSpace(event)
	dir := env.GetString("dir", "")
	if dir == "" {
		panic("no dir")
	}
	script := env.GetString("script", "")
	if script == "" {
		panic("no script")
	}
	fmt.Printf("started with event: %s\n", event)
	// Nats
	url := env.GetString("NATS_URL", "nats://nats:4222")
	fmt.Printf("Connecting Nats with %s\n", url)

	nc, err := nats.Connect("nats://nats:4222", nats.Name("Worker"))
	if err != nil {
		fmt.Printf("cannot connect to NATS server: %v", err)
		os.Exit(1)
	}

	defer nc.Drain()

	js, err := jetstream.New(nc)
	if err != nil {
		fmt.Printf("cannot create JetStream instance: %v", err)
		os.Exit(1)
	}

	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:      "EVENTS-" + event,
		Subjects:  []string{event},
		Retention: jetstream.WorkQueuePolicy,
	})
	if err != nil {
		fmt.Printf("cannot create stream: %v", err)
		os.Exit(1)
	}
	// https://github.com/mdawar/jetstream-api-demos/blob/main/shutdown/cmd/messages/main.go
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "JobsConsumer",
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       5 * time.Minute,
		MaxDeliver:    1,
		MaxAckPending: -1,
	})
	if err != nil {
		fmt.Printf("cannot create consumer: %v", err)
		os.Exit(1)
	}

	fmt.Println("Consumer ID:", consumer.CachedInfo().Name)

	iter, err := consumer.Messages(
		jetstream.PullMaxMessages(1),
		jetstream.WithMessagesErrOnMissingHeartbeat(true),
	)
	if err != nil {
		fmt.Printf("cannot consume messages: %v", err)
		os.Exit(1)
	}

	defer iter.Stop()

	var scriptType scriptType
	if strings.HasSuffix(script, "py") {
		scriptType = ScriptPython
	} else {
		if strings.HasSuffix(script, "js") {
			scriptType = ScriptJavascript
		} else {
			fmt.Printf("file %s unknown", script)
			os.Exit(1)
		}
	}

	cacheDir := path.Join(dir, "/.cache")
	var mod *wasm.GenericModule
	var wasm string
	switch scriptType {
	case ScriptPython:
		mod, wasm, err = modForPython(ctx, dir, cacheDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating python module: %v\n", err)
			os.Exit(1)
		}
	case ScriptJavascript:
	}
	if scriptType == ScriptJavascript {
		mod, wasm, err = modForJavascript(ctx, dir, cacheDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating javascript module: %v\n", err)
			os.Exit(1)
		}
	}
	defer mod.Close(ctx)

	parallelConsumers := 1
	sem := make(chan struct{}, parallelConsumers)
	for {
		sem <- struct{}{}

		go func() {
			defer func() {
				<-sem
			}()

			msg, err := iter.Next()
			if err != nil {
				fmt.Println("next err: ", err)
				return
			}
			fmt.Printf("Received msg: %s\n", msg.Data())
			buffIn := &bytes.Buffer{}
			buffOut := &bytes.Buffer{}
			buffErr := &bytes.Buffer{}
			_, err = buffIn.Write(msg.Data())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error writting data: %v\n", err)
			} else {
				ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				var err error
				if scriptType == ScriptJavascript {
					err = runJavascript(ctx, mod, dir, wasm, script, buffIn, buffOut, buffErr)
				}
				if scriptType == ScriptPython {
					err = runPython(ctx, mod, dir, wasm, script, buffIn, buffOut, buffErr)
				}
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error executing the module: %v\n", err)
					fmt.Printf("Error: %s\n", buffErr.String())
					fmt.Printf("Std out: %s\n", buffOut.String())
				} else {
					if scriptType == ScriptPython {
						if err := processPythonOutput(buffOut, buffErr); err != nil {
							fmt.Fprintf(os.Stderr, "Error while processing the error results: %v\n", err)
						}
					}
					if scriptType == ScriptJavascript {
						if err := processJavascriptOutput(buffOut, buffErr); err != nil {
							fmt.Fprintf(os.Stderr, "Error while processing the error results: %v\n", err)
						}
					}
				}
			}
			msg.Ack()
		}()
	}
}

func processPythonOutput(buffOut *bytes.Buffer, buffErr *bytes.Buffer) error {
	var res int32
	binary.Read(buffOut, binary.LittleEndian, &res)
	fmt.Printf("Result:\n%d\n", res)
	msgRes, err := io.ReadAll(buffOut)
	if err != nil {
		return fmt.Errorf("error reading out buffer: %v", err)
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
		return fmt.Errorf("error reading log buffer: %v", err)
	}
	fmt.Printf("Text:%s\n", msgLog)
	return nil
}

func processJavascriptOutput(buffOut *bytes.Buffer, buffErr *bytes.Buffer) error {
	scErr := bufio.NewScanner(buffErr)
	for scErr.Scan() {
		msgErr := scErr.Text()
		fmt.Printf("Level:%s\n", string(msgErr[0]))
		fmt.Printf("Text:%s\n", msgErr[1:])
	}
	scOut := bufio.NewScanner(buffOut)
	if scOut.Scan() {
		msgOut := scOut.Text()
		fmt.Printf("Result:%s\n", strings.TrimSpace(msgOut[0:11]))
		fmt.Printf("Text:%s\n", msgOut[11:])
	}
	return nil
}

func runPython(ctx context.Context, mod *wasm.GenericModule, dir, pywasm, script string, buffIn *bytes.Buffer, buffOut *bytes.Buffer, buffErr *bytes.Buffer) error {
	mounts := []string{
		path.Join(dir, "python/lib/python3.13") + ":/usr/local/lib/python3.13:ro",
		path.Join(dir, "python/sdk") + ":/usr/local/lib/jobico:ro",
		path.Join(dir, "python/prg") + ":/prg",
	}
	args := []string{
		pywasm,
		"/prg/" + script,
	}
	e := []wasm.EnvVar{{Key: "PYTHONPATH", Value: "/usr/local/lib/jobico"}}
	return mod.Run(ctx, mounts, args, e, buffIn, buffOut, buffErr)
}

func runJavascript(ctx context.Context, mod *wasm.GenericModule, dir, jswasm, script string, buffIn *bytes.Buffer, buffOut *bytes.Buffer, buffErr *bytes.Buffer) error {
	fmt.Printf("WASM: %s\n", jswasm)
	fmt.Printf("Script: %s\n", script)
	fmt.Printf("Dir: %s\n", dir)
	mounts := []string{
		path.Join(dir, "/js") + ":/js",
	}
	args := []string{
		jswasm,
		"--module=/js/prg/" + script,
	}
	fmt.Printf("%v\n", args)
	e := []wasm.EnvVar{}
	return mod.Run(ctx, mounts, args, e, buffIn, buffOut, buffErr)
}

func modForPython(ctx context.Context, dir string, tempDir string) (*wasm.GenericModule, string, error) {
	pywasm := path.Join(dir, "/python/python.wasm")
	wasmf, err := os.ReadFile(pywasm)
	if err != nil {
		return nil, "", err
	}
	mod, err := wasm.NewGenericModule(ctx, tempDir, wasmf, log)
	if err != nil {
		return nil, "", err
	}
	return mod, "/python/python.wasm", nil
}

func modForJavascript(ctx context.Context, dir string, tempDir string) (*wasm.GenericModule, string, error) {
	pywasm := path.Join(dir, "/js/js.wasm")
	wasmf, err := os.ReadFile(pywasm)
	if err != nil {
		return nil, "", err
	}
	mod, err := wasm.NewGenericModule(ctx, tempDir, wasmf, log)
	if err != nil {
		return nil, "", err
	}
	return mod, "/js/js.wasm", nil
}

func log(ctx context.Context, lvl uint32, msg string) error {
	fmt.Printf("%d-%s", lvl, msg)
	return nil
}

// nc, err := nats.Connect(url)
// if err != nil {
// 	panic(err)
// }
// defer nc.Drain()
// js, err := jetstream.New(nc)
// if err != nil {
// 	panic(err)
// }
// cfg := jetstream.StreamConfig{
// 	Name:      "EVENTS-" + event,
// 	Retention: jetstream.WorkQueuePolicy,
// 	Subjects:  []string{event},
// }

// // JetStream API uses context for timeouts and cancellation.
// stream, err := js.CreateOrUpdateStream(context.Background(), cfg)
// if err != nil {
// 	panic(err)
// }
// consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
// 	Name:          "processor-1",
// 	ReplayPolicy:  jetstream.ReplayInstantPolicy,
// 	AckPolicy:     jetstream.AckExplicitPolicy,
// 	AckWait:       5 * time.Minute,
// 	MaxAckPending: -1,
// })
// if err != nil {
// 	panic(err)
// }

// fmt.Println("created the stream")
// var scriptType scriptType
// if strings.HasSuffix(script, "py") {
// 	scriptType = ScriptPython
// } else {
// 	if strings.HasSuffix(script, "js") {
// 		scriptType = ScriptJavascript
// 	} else {
// 		fmt.Printf("file %s unknown", script)
// 		os.Exit(1)
// 	}
// }

// cacheDir := path.Join(dir, "/.cache")
// runtime, err := wasm.NewRuntimeWithCompilationCache(cacheDir)
// if err != nil {
// 	fmt.Fprintf(os.Stderr, "error initializing Wazero: %v\n", err)
// 	os.Exit(1)
// }
// defer runtime.Close(ctx)
// buffIn := &bytes.Buffer{}
// buffOut := &bytes.Buffer{}
// buffErr := &bytes.Buffer{}
// var mod *wasm.IntModule
// switch scriptType {
// case ScriptPython:
// 	mod, err = modForPython(ctx, dir, script, runtime, buffIn, buffOut, buffErr)
// 	if err != nil {
// 		fmt.Fprintf(os.Stderr, "error creating python module: %v\n", err)
// 		os.Exit(1)
// 	}
// case ScriptJavascript:
// }
// if scriptType == ScriptJavascript {
// 	mod, err = modForJavascript(ctx, dir, script, runtime, buffIn, buffOut, buffErr)
// 	if err != nil {
// 		fmt.Fprintf(os.Stderr, "error creating javascript module: %v\n", err)
// 		os.Exit(1)
// 	}
// }
// defer mod.Close(ctx)
// iter, err := consumer.Messages(
// 	jetstream.PullMaxMessages(1),
// 	jetstream.WithMessagesErrOnMissingHeartbeat(true), // Added for consistency with Consume()
// )
// if err != nil {
// 	fmt.Fprintf(os.Stderr, "error consuming: %v\n", err)
// 	os.Exit(1)
// }
// defer iter.Stop()
// parallelConsumers := 5
// sem := make(chan struct{}, parallelConsumers)

// for {
// 	sem <- struct{}{}

// 	go func() {
// 		defer func() {
// 			<-sem
// 		}()

// 		msg, err := iter.Next()
// 		if err != nil {
// 			fmt.Println("next err: ", err)
// 			return
// 		}
// 		data := msg.Data()
// 		fmt.Printf("Received msg: %s\n", data)
// 		buffErr.Reset()
// 		buffIn.Reset()
// 		buffOut.Reset()
// 		_, err = buffIn.Write(msg.Data())
// 		if err != nil {
// 			fmt.Fprintf(os.Stderr, "error writting data: %v\n", err)
// 		} else {
// 			err = mod.Run(ctx)
// 			if err != nil {
// 				fmt.Fprintf(os.Stderr, "Error executing the module: %v\n", err)
// 				fmt.Printf("Error: %s\n", buffErr.String())
// 				fmt.Printf("Std out: %s\n", buffOut.String())
// 			} else {
// 				if scriptType == ScriptPython {
// 					if err := processPythonOutput(buffOut, buffErr); err != nil {
// 						fmt.Fprintf(os.Stderr, "Error while processing the error results: %v\n", err)
// 					}
// 				}
// 				if scriptType == ScriptJavascript {
// 					if err := processJavascriptOutput(buffOut, buffErr); err != nil {
// 						fmt.Fprintf(os.Stderr, "Error while processing the error results: %v\n", err)
// 					}
// 				}
// 			}
// 		}
// 		msg.Ack()
// 	}()
//}
// loop:
//
//	for {
//		select {
//		case <-ctx.Done():
//			break loop
//		default:
//			fmt.Printf("getting message from %s\n", event)
//			msgs, err := cons.Fetch(3)
//			if err != nil {
//				fmt.Printf("error: %v", err)
//			} else {
//				for msg := range msgs.Messages() {
//					fmt.Println("executing ....")
//					msg.DoubleAck(ctx)
//					buffErr.Reset()
//					buffIn.Reset()
//					buffOut.Reset()
//					_, err := buffIn.Write(msg.Data())
//					if err != nil {
//						fmt.Fprintf(os.Stderr, "error writting data: %v\n", err)
//						continue
//					}
//					err = mod.Run(ctx)
//					if err != nil {
//						fmt.Fprintf(os.Stderr, "Error executing the module: %v\n", err)
//						fmt.Printf("Error: %s\n", buffErr.String())
//						fmt.Printf("Std out: %s\n", buffOut.String())
//						continue
//					}
//					if scriptType == ScriptPython {
//						if err := processPythonOutput(buffOut, buffErr); err != nil {
//							fmt.Fprintf(os.Stderr, "Error while processing the error results: %v\n", err)
//						}
//					}
//					if scriptType == ScriptJavascript {
//						if err := processJavascriptOutput(buffOut, buffErr); err != nil {
//							fmt.Fprintf(os.Stderr, "Error while processing the error results: %v\n", err)
//						}
//					}
//				}
//				if msgs.Error() != nil {
//					fmt.Printf("error feching: %v\n", msgs.Error())
//				}
//			}
//			fmt.Println("sleeping")
//			time.Sleep(1 * time.Second)
//		}
//	}
//}
