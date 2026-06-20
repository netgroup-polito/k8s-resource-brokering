package main

import (
	"flag"
	"log"

	"github.com/mehdiazizian/mock-currency/internal/api"
)

func main() {
	port := flag.Int("port", 8082, "Port to listen on")
	flag.Parse()

	log.Printf("mock-currency starting...")
	if err := api.StartServer(*port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
