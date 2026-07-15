package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"examcoo-cloud/internal/api"
	"examcoo-cloud/internal/core"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize storage (Redis or file)
	core.InitStorage()

	hub := api.NewSseHub()
	app := api.NewApp(hub)
	router := api.NewRouter(app, hub)

	addr := ":" + port
	fmt.Printf("ExamCoo Cloud Server listening on %s\n", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
