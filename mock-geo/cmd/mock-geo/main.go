package main

import (
	"flag"
	"log"

	"github.com/mehdiazizian/mock-geo/internal/api"
)

func main() {
	port := flag.Int("port", 8080, "Port to listen on")
	flag.Parse()

	log.Printf("mock-geo starting...")
	if err := api.StartServer(*port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
