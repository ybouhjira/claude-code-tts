package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ybouhjira/claude-code-tts/internal/server"
)

func main() {
	// Check for required environment variable
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Create and start the MCP server
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down TTS server...")
		srv.Shutdown()
		os.Exit(0)
	}()

	// Start serving
	log.Println("Starting Claude Code TTS MCP Server...")
	if err := srv.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
