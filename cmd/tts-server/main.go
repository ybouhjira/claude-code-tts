package main

import (
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/ybouhjira/claude-code-tts/internal/logging"
	"github.com/ybouhjira/claude-code-tts/internal/server"
)

func main() {
	// Initialize file logging
	if err := logging.Init(); err != nil {
		log.Printf("Warning: failed to initialize file logging: %v", err)
	}

	logging.Info("========================================")
	logging.Info("TTS Server Starting")
	logging.Info("Go version: %s", runtime.Version())
	logging.Info("OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logging.Info("PID: %d", os.Getpid())
	logging.Info("Log file: %s", logging.GetLogPath())
	logging.Info("========================================")

	// At least one provider API key must be configured
	hasOpenAI := os.Getenv("OPENAI_API_KEY") != ""
	hasElevenLabs := os.Getenv("ELEVENLABS_API_KEY") != ""
	if !hasOpenAI && !hasElevenLabs {
		logging.Fatal("at least one API key is required: set OPENAI_API_KEY and/or ELEVENLABS_API_KEY")
	}
	if hasOpenAI {
		logging.Info("OPENAI_API_KEY is set (length: %d)", len(os.Getenv("OPENAI_API_KEY")))
	}
	if hasElevenLabs {
		logging.Info("ELEVENLABS_API_KEY is set (length: %d)", len(os.Getenv("ELEVENLABS_API_KEY")))
	}

	// Create and start the MCP server
	srv, err := server.New()
	if err != nil {
		logging.Fatal("Failed to create server: %v", err)
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGPIPE)

	go func() {
		sig := <-sigChan
		logging.Info("Received signal: %v", sig)
		logging.Info("Shutting down TTS server...")
		srv.Shutdown()
		logging.Info("TTS Server stopped gracefully")
		os.Exit(0)
	}()

	// Start serving
	logging.Info("Starting MCP stdio server...")
	if err := srv.Start(); err != nil {
		logging.Error("Server error: %v", err)
		logging.Fatal("Server stopped unexpectedly")
	}

	logging.Info("Server ended normally")
}
