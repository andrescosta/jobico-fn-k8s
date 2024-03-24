package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"k8s.io/utils/env"
)

type apiHandler struct {
	Schema *jsonschema.Schema
}

type MerchantData struct {
	Data []interface{}
}

func (a *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	event := MerchantData{}
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "Request body illegal", http.StatusBadRequest)
		return
	}
	for _, e := range event.Data {
		if err := a.Schema.Validate(e); err != nil {
			fmt.Printf("%v", err)
			http.Error(w, "Request body illegal", http.StatusBadRequest)
		}
	}
	fmt.Fprintf(w, "OKKK")
}

func main() {
	event := env.GetString("event", "")
	if event == "" {
		event = "def"
	}
	res := fmt.Sprintf("/%s/", event)
	mux := http.NewServeMux()

	comp := jsonschema.NewCompiler()
	file := "/etc/listener/schema-" + event + ".json"
	fmt.Printf("reading file: %s\n", file)
	bs, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	if err := comp.AddResource("schema", bytes.NewReader(bs)); err != nil {
		panic(err)
	}
	compiledSchema, err := comp.Compile("schema")
	if err != nil {
		panic(err)
	}
	h := apiHandler{
		Schema: compiledSchema,
	}
	mux.Handle(res, &h)
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		fmt.Fprintf(w, "Service for %s!", event)
	})

	var srv http.Server
	idleConnsClosed := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		if err := srv.Shutdown(context.Background()); err != nil {
			fmt.Printf("HTTP server Shutdown: %v", err)
		}
		close(idleConnsClosed)
	}()

	srv = http.Server{Addr: ":8080", Handler: mux}
	fmt.Printf("listening %s on 8080\n", event)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Printf("HTTP server ListenAndServe: %v", err)
	}

	<-idleConnsClosed
	println("stopped")
}
