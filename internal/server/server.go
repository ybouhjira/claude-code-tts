package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ybouhjira/claude-code-tts/internal/logging"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// Server wraps the MCP server and worker pool
type Server struct {
	mcpServer  *server.MCPServer
	workerPool *WorkerPool
}

// New creates a new TTS MCP server
func New() (*Server, error) {
	logging.Info("Creating TTS MCP server...")

	// Create worker pool (2 workers, queue size 50)
	wp := NewWorkerPool(2, 50)
	wp.Start()
	logging.Info("Worker pool created and started")

	// Create MCP server
	mcpSrv := server.NewMCPServer(
		"claude-code-tts",
		"1.0.0",
		server.WithToolCapabilities(true),
	)
	logging.Info("MCP server instance created")

	s := &Server{
		mcpServer:  mcpSrv,
		workerPool: wp,
	}

	// Register tools
	s.registerTools()
	logging.Info("Tools registered: speak, tts_status")

	return s, nil
}

// registerTools adds the TTS tools to the MCP server
func (s *Server) registerTools() {
	// speak tool - converts text to speech
	speakTool := mcp.NewTool("speak",
		mcp.WithDescription("Convert text to speech and play it aloud. Use this to provide audio feedback to the user."),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("The text to convert to speech (max 4096 characters)"),
		),
		mcp.WithString("voice",
			mcp.Description("Voice to use: alloy, echo, fable, onyx, nova, shimmer (default: alloy)"),
		),
	)

	s.mcpServer.AddTool(speakTool, s.handleSpeak)

	// tts_status tool - returns worker pool status
	statusTool := mcp.NewTool("tts_status",
		mcp.WithDescription("Get the current status of the TTS system including queue size, processed count, and recent jobs."),
	)

	s.mcpServer.AddTool(statusTool, s.handleStatus)
}

// handleSpeak processes speak tool calls
func (s *Server) handleSpeak(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	logging.Debug("Received speak tool call")

	// Extract text parameter
	text, ok := request.Params.Arguments["text"].(string)
	if !ok || text == "" {
		logging.Warn("speak: missing or empty text parameter")
		return mcp.NewToolResultError("text parameter is required"), nil
	}

	// Validate text length
	if len(text) > 4096 {
		logging.Warn("speak: text exceeds max length (%d chars)", len(text))
		return mcp.NewToolResultError("text exceeds maximum length of 4096 characters"), nil
	}

	// Extract voice parameter (default to alloy)
	voice := "alloy"
	if v, ok := request.Params.Arguments["voice"].(string); ok && v != "" {
		voice = v
	}

	// Validate voice
	if !tts.IsValidVoice(voice) {
		logging.Warn("speak: invalid voice '%s'", voice)
		return mcp.NewToolResultError(fmt.Sprintf("invalid voice '%s'. Valid voices: alloy, echo, fable, onyx, nova, shimmer", voice)), nil
	}

	logging.Info("speak: queueing job (voice=%s, text_len=%d, preview='%.50s...')", voice, len(text), text)

	// Submit job to worker pool
	job, err := s.workerPool.Submit(text, tts.Voice(voice))
	if err != nil {
		logging.Error("speak: failed to queue job: %v", err)
		return mcp.NewToolResultError(fmt.Sprintf("failed to queue TTS job: %v", err)), nil
	}

	logging.Info("speak: job queued successfully (ID: %s)", job.ID)
	return mcp.NewToolResultText(fmt.Sprintf("TTS job queued successfully (ID: %s, voice: %s)", job.ID, voice)), nil
}

// handleStatus processes tts_status tool calls
func (s *Server) handleStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	logging.Debug("Received tts_status tool call")
	status := s.workerPool.GetStatus()

	jsonData, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		logging.Error("tts_status: failed to marshal: %v", err)
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal status: %v", err)), nil
	}

	logging.Debug("tts_status: processed=%d, failed=%d, pending=%d",
		status.TotalProcessed, status.TotalFailed, status.QueuePending)
	return mcp.NewToolResultText(string(jsonData)), nil
}

// Start begins serving MCP requests via stdio
func (s *Server) Start() error {
	logging.Info("Starting stdio server (blocking)...")
	err := server.ServeStdio(s.mcpServer)
	if err != nil {
		logging.Error("ServeStdio returned error: %v", err)
	} else {
		logging.Info("ServeStdio returned without error")
	}
	return err
}

// Shutdown gracefully stops the server
func (s *Server) Shutdown() {
	logging.Info("Server shutdown initiated...")
	s.workerPool.Stop()
	logging.Info("Server shutdown complete")
}
