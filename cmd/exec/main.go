package main

import (
	"fmt"
	"os"

	"github.com/andrescosta/jobicok8s/cmd/runner"
)

func main() {
	svc, err := runner.New()
	if err != nil {
		fmt.Printf("Error new runner: %v\n", err)
		os.Exit(1)
	}
	if err := svc.Run(); err != nil {
		fmt.Printf("Error start runner: %v\n", err)
		os.Exit(1)
	}
}
