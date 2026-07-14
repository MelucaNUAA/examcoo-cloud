package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"examcoo-cloud/internal/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	hub := api.NewWsHub()
	app := api.NewApp(hub)
	router := api.NewRouter(app, hub)

	addr := ":" + port
	fmt.Printf("ExamCoo Cloud Server listening on %s\n", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
