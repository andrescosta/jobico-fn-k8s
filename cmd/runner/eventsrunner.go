package runner

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"k8s.io/utils/env"
)

type EventsRunner struct {
	ctx      context.Context
	cancel   context.CancelFunc
	natsURL  string
	event    string
	dir      string
	cacheDir string
	wasmFile string
	mod      moduleRunner
}
type Options struct {
	wasmFile string
	script   string
	event    string
	dir      string
	natsURL  string
}

func New() (*EventsRunner, error) {
	return NewWithOptions(nil)
}

func NewWithOptions(opts *Options) (*EventsRunner, error) {
	ctx, cancel := contextForSignals()
	o := optionsFromEnvVars()
	o.merge(opts)
	if err := o.validate(); err != nil {
		return nil, err
	}
	fmt.Printf("%s-%s-%s-%s-%s\n", o.script, o.wasmFile, o.dir, o.event, o.natsURL)
	cacheDir := path.Join(o.dir, ".cache")
	svc := &EventsRunner{
		ctx:      ctx,
		cancel:   cancel,
		natsURL:  o.natsURL,
		event:    o.event,
		dir:      o.dir,
		cacheDir: cacheDir,
	}
	if len(o.script) != 0 {
		fmt.Println("About to create module runner ")
		m, err := newGenericModuleRunner(ctx, o.script, svc, log)
		if err != nil {
			return nil, err
		}
		svc.mod = m
	}
	if len(o.wasmFile) != 0 {
		fmt.Println("creating jobicolet")
		svc.wasmFile = o.wasmFile
		m, err := NewjobicoletModuleRunner(ctx, svc.wasmFile, svc, log)
		if err != nil {
			return nil, err
		}
		svc.mod = m
	}
	return svc, nil
}

func (s *EventsRunner) Run() (err error) {
	fmt.Println("starting gettings messages")
	nc, iter, errn := s.connectNat()
	if err != nil {
		err = errn
		return
	}
	defer func() {
		err = errors.Join(err, nc.Drain())
	}()
	parallelConsumers := 1
	wg := sync.WaitGroup{}
	for range parallelConsumers {
		go func() {
			defer wg.Done()
			fmt.Println("consumer started")
			for {
				msg, err := iter.Next()
				if err != nil {
					if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
						break
					}
					fmt.Printf("%v\n", err)
				} else {
					fmt.Printf("Received msg: %s\n", msg.Data())
					res, msgr, err := s.mod.run(s.ctx, msg.Data())
					if err != nil {
						er, k := err.(errorRun)
						if k {
							fmt.Printf("Err: %v\n", er.Err)
							fmt.Printf("Std out: %s\n", string(er.StdOut))
							fmt.Printf("Std err: %s\n", string(er.StdErr))
						} else {
							fmt.Printf("Error executing: %v\n", err)
						}
					} else {
						fmt.Printf("Result from the call: %d-%s\n", res, msgr)
					}
				}
				msg.Ack()
			}
		}()
	}
	<-s.ctx.Done()
	iter.Stop()
	wg.Wait()
	return nil
}

func (s *EventsRunner) connectNat() (*nats.Conn, jetstream.MessagesContext, error) {
	nc, err := nats.Connect(s.natsURL, nats.Name("Worker"))
	if err != nil {
		return nil, nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, nil, err
	}
	stream, err := js.CreateStream(s.ctx, jetstream.StreamConfig{
		Name:      "EVENTS-" + s.event,
		Subjects:  []string{s.event},
		Retention: jetstream.WorkQueuePolicy,
	})
	if err != nil {
		return nil, nil, err
	}
	consumer, err := stream.CreateOrUpdateConsumer(s.ctx, jetstream.ConsumerConfig{
		Durable:       "JobsConsumer",
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       5 * time.Minute,
		MaxDeliver:    1,
		MaxAckPending: -1,
	})
	if err != nil {
		return nil, nil, err
	}
	iter, err := consumer.Messages(
		jetstream.PullMaxMessages(1),
		jetstream.WithMessagesErrOnMissingHeartbeat(true),
	)
	if err != nil {
		return nil, nil, err
	}
	return nc, iter, nil
}

func log(ctx context.Context, lvl uint32, msg string) error {
	fmt.Printf("[%d]-%s\n", lvl, msg)
	return nil
}

func contextForSignals() (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	return ctx, cancel
}

func (o *Options) merge(opts *Options) {
	if opts == nil {
		return
	}
	o.wasmFile = OtherIfNotNil(o.wasmFile, opts.wasmFile)
	o.script = OtherIfNotNil(o.script, opts.script)
	o.script = OtherIfNotNil(o.script, opts.event)
	o.dir = OtherIfNotNil(o.dir, opts.dir)
	o.natsURL = OtherIfNotNil(o.natsURL, opts.natsURL)
}

func optionsFromEnvVars() Options {
	return Options{
		wasmFile: env.GetString("wasm", ""),
		script:   env.GetString("script", ""),
		event:    strings.TrimSpace(env.GetString("event", "")),
		dir:      env.GetString("dir", ""),
		natsURL:  env.GetString("NATS_URL", "nats://queue:4222"),
	}
}

func (o *Options) validate() error {
	if IsZero(o.script) && IsZero(o.wasmFile) {
		return errors.New("script or wasmFile must be provided")
	}
	if !IsZero(o.script) && !IsZero(o.wasmFile) {
		return errors.New("script and wasmFile provided")
	}
	if IsZero(o.event) {
		return errors.New("event is empty")
	}
	if IsZero(o.dir) {
		return errors.New("dir is empty")
	}
	return nil
}

func OtherIfNotNil[T comparable](value T, other T) T {
	if !IsZero(other) {
		return other
	}
	return value
}

func ValueIfNotNil[T comparable](value T, other T) T {
	if !IsZero(value) {
		return value
	}
	return other
}

func IsZero[T comparable](v T) bool {
	return v == *new(T)
}
