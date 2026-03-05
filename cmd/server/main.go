package main

import (
	"fmt"
	"log"
	"os"

	"github.com/smkbarbosa/context-ia-manager/internal/api"
	"github.com/smkbarbosa/context-ia-manager/internal/config"
)

func main() {
	cfg := config.Load()

	srv, err := api.NewServer(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to initialise server:", err)
		log.Fatal(err)
	}

	addr := ":8080"
	if err := srv.ListenAndServe(addr); err != nil {
		log.Fatal(err)
	}
}
