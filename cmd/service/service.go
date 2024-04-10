package service

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

type modulesType uint8

const (
	ModuleJobicolet modulesType = iota
	ModuleGeneric
)

type Service struct {
	ctx        context.Context
	cancel     context.CancelFunc
	natsURL    string
	event      string
	dir        string
	cacheDir   string
	wasmFile   string
	moduleType modulesType
	mod        moduleRunner
}

func main() {
	// Read config files (common)
	svc, err := NewService()
	if err != nil {
		panic(err)
	}
	svc.Start()
	// Instantiate the module (custom)

	// Execute and manage results (custom)
}

func NewService() (*Service, error) {
	ctx, cancel := Context()
	wasmFile := env.GetString("wasm", "")
	script := env.GetString("script", "")
	if len(script) == 0 && len(wasmFile) == 0 {
		return nil, errors.New("")
	}
	if len(script) != 0 && len(wasmFile) != 0 {
		return nil, errors.New("")
	}
	event := env.GetString("event", "")
	if event == "" {
		event = "def"
	}
	event = strings.TrimSpace(event)
	dir := env.GetString("dir", "")
	if dir == "" {
		panic("no dir")
	}
	natsUrl := env.GetString("NATS_URL", "nats://queue:4222")
	cacheDir := path.Join(dir, ".cache")
	svc := Service{
		ctx:      ctx,
		cancel:   cancel,
		natsURL:  natsUrl,
		event:    event,
		dir:      dir,
		cacheDir: cacheDir,
		wasmFile: wasmFile,
	}
	var m moduleRunner
	var err error
	if len(script) > 0 {
		m, err = NewGenericModuleRunner(ctx, script, svc, log)
	} else {
		m, err = NewjobicoletModuleRunner(ctx, wasmFile, svc, log)
	}
	if err != nil {
		return nil, err
	}
	svc.mod = m
	return &svc, nil
}

func (s *Service) Start() (err error) {
	nc, iter, err := s.connectNat()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, nc.Drain())
	}()
	parallelConsumers := 1
	wg := sync.WaitGroup{}
	sem := make(chan struct{}, parallelConsumers)
loop:
	for {
		select {
		case <-s.ctx.Done():
			break loop
		case sem <- struct{}{}:
			wg.Add(1)
			go func() {
				defer func() {
					wg.Done()
					<-sem
				}()
				msg, err := iter.Next()
				if err != nil {
					if !errors.Is(err, jetstream.ErrMsgIteratorClosed) {
						fmt.Printf("%v\n", err)
					}
					return
				}
				fmt.Printf("Received msg: %s\n", msg.Data())
				defer msg.Ack()
				res, msgr, err := s.mod.execute(s.ctx, msg.Data())
				fmt.Printf("%d-%s-%v\n", res, msgr, err)
			}()
		}
	}
	iter.Stop()
	wg.Wait()
	return nil
}

func (s *Service) connectNat() (*nats.Conn, jetstream.MessagesContext, error) {
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
	fmt.Println("Consumer ID:", consumer.CachedInfo().Name)
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
	fmt.Printf("%d-%s", lvl, msg)
	return nil
}

func Context() (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	return ctx, cancel
}
