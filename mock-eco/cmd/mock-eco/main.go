package main

import (
	"flag"
	"log"

	"github.com/mehdiazizian/mock-eco/internal/api"
)

func main() {
	port := flag.Int("port", 8081, "Port to listen on")
	flag.Parse()

	log.Printf("mock-eco starting...")
	if err := api.StartServer(*port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
