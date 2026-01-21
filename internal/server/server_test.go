package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestNew(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv == nil {
		t.Fatal("expected server to be created")
	}
	if srv.mcpServer == nil {
		t.Error("expected mcpServer to be initialized")
	}
	if srv.workerPool == nil {
		t.Error("expected workerPool to be initialized")
	}

	// Clean up
	srv.Shutdown()
}

func TestHandleSpeak_Success(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Hello, world!",
		"voice": "nova",
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if result.IsError {
		t.Errorf("expected success, got error: %v", result.Content)
	}

	// Check that the result contains job ID
	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "job-") {
		t.Errorf("expected result to contain job ID, got: %s", content.Text)
	}
	if !strings.Contains(content.Text, "nova") {
		t.Errorf("expected result to mention voice, got: %s", content.Text)
	}
}

func TestHandleSpeak_DefaultVoice(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": "Hello without voice",
		// no voice specified - should default to alloy
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("expected success, got error")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "alloy") {
		t.Errorf("expected default voice 'alloy' in result, got: %s", content.Text)
	}
}

func TestHandleSpeak_MissingText(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"voice": "nova",
		// text is missing
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing text")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "text parameter is required") {
		t.Errorf("expected 'text parameter is required' error, got: %s", content.Text)
	}
}

func TestHandleSpeak_EmptyText(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": "",
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for empty text")
	}
}

func TestHandleSpeak_TextTooLong(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	// Create text longer than 4096 chars
	longText := strings.Repeat("a", 4097)

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": longText,
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for text too long")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "4096") {
		t.Errorf("expected error to mention 4096 limit, got: %s", content.Text)
	}
}

func TestHandleSpeak_InvalidVoice(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Hello",
		"voice": "invalid-voice",
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid voice")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "invalid voice") {
		t.Errorf("expected 'invalid voice' error, got: %s", content.Text)
	}
}

func TestHandleStatus(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}

	result, err := srv.handleStatus(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if result.IsError {
		t.Error("expected success")
	}

	// Parse the JSON response
	content := result.Content[0].(mcp.TextContent)
	var status PoolStatus
	if err := json.Unmarshal([]byte(content.Text), &status); err != nil {
		t.Fatalf("failed to parse status JSON: %v", err)
	}

	// Verify default values
	if status.WorkerCount != 2 {
		t.Errorf("expected worker_count 2, got %d", status.WorkerCount)
	}
	if status.QueueSize != 50 {
		t.Errorf("expected queue_size 50, got %d", status.QueueSize)
	}
}

func TestHandleStatus_AfterJobs(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	// Submit a job first
	speakRequest := mcp.CallToolRequest{}
	speakRequest.Params.Arguments = map[string]interface{}{
		"text": "Test message",
	}
	_, _ = srv.handleSpeak(context.Background(), speakRequest)

	// Now check status
	statusRequest := mcp.CallToolRequest{}
	result, err := srv.handleStatus(context.Background(), statusRequest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := result.Content[0].(mcp.TextContent)
	var status PoolStatus
	if err := json.Unmarshal([]byte(content.Text), &status); err != nil {
		t.Fatalf("failed to parse status JSON: %v", err)
	}

	// Job may be pending or already processed (race condition in tests)
	// Just verify recent jobs contains the job we submitted
	if len(status.RecentJobs) < 1 {
		t.Errorf("expected at least 1 recent job, got %d", len(status.RecentJobs))
	}
}

func TestServer_Shutdown(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Shutdown should not panic or hang
	done := make(chan struct{})
	go func() {
		srv.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Shutdown timed out")
	}
}

// Table-driven tests for handleSpeak with various inputs
func TestHandleSpeak_TableDriven(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	tests := []struct {
		name        string
		text        string
		voice       string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid with alloy voice",
			text:        "Hello, world!",
			voice:       "alloy",
			expectError: false,
		},
		{
			name:        "valid with echo voice",
			text:        "Test message",
			voice:       "echo",
			expectError: false,
		},
		{
			name:        "valid with fable voice",
			text:        "Another test",
			voice:       "fable",
			expectError: false,
		},
		{
			name:        "valid with onyx voice",
			text:        "Yet another test",
			voice:       "onyx",
			expectError: false,
		},
		{
			name:        "valid with nova voice",
			text:        "Nova voice test",
			voice:       "nova",
			expectError: false,
		},
		{
			name:        "valid with shimmer voice",
			text:        "Shimmer voice test",
			voice:       "shimmer",
			expectError: false,
		},
		{
			name:        "missing text",
			text:        "",
			voice:       "alloy",
			expectError: true,
			errorMsg:    "text parameter is required",
		},
		{
			name:        "text too long",
			text:        strings.Repeat("a", 4097),
			voice:       "alloy",
			expectError: true,
			errorMsg:    "4096",
		},
		{
			name:        "invalid voice",
			text:        "Hello",
			voice:       "invalid-voice",
			expectError: true,
			errorMsg:    "invalid voice",
		},
		{
			name:        "exactly 4096 chars (boundary test)",
			text:        strings.Repeat("a", 4096),
			voice:       "alloy",
			expectError: false,
		},
		{
			name:        "one char (boundary test)",
			text:        "a",
			voice:       "alloy",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{
				"text":  tt.text,
				"voice": tt.voice,
			}

			result, err := srv.handleSpeak(context.Background(), request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectError {
				if !result.IsError {
					t.Errorf("expected error but got success")
				}
				if tt.errorMsg != "" {
					content := result.Content[0].(mcp.TextContent)
					if !strings.Contains(content.Text, tt.errorMsg) {
						t.Errorf("expected error message to contain '%s', got: %s", tt.errorMsg, content.Text)
					}
				}
			} else {
				if result.IsError {
					content := result.Content[0].(mcp.TextContent)
					t.Errorf("expected success but got error: %s", content.Text)
				}
			}
		})
	}
}

func TestHandleStatus_ReturnsValidJSON(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	result, err := srv.handleStatus(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Error("expected success")
	}

	// Verify JSON is valid and contains expected fields
	content := result.Content[0].(mcp.TextContent)
	var status PoolStatus
	if err := json.Unmarshal([]byte(content.Text), &status); err != nil {
		t.Fatalf("failed to parse status JSON: %v", err)
	}

	// Verify all expected fields are present
	if status.WorkerCount == 0 {
		t.Error("worker_count should not be 0")
	}
	if status.QueueSize == 0 {
		t.Error("queue_size should not be 0")
	}
	// Other fields can be 0 initially
	if status.TotalProcessed < 0 {
		t.Error("total_processed should not be negative")
	}
	if status.TotalFailed < 0 {
		t.Error("total_failed should not be negative")
	}
	if status.QueuePending < 0 {
		t.Error("queue_pending should not be negative")
	}
}

func TestHandleSpeak_NonStringVoiceType(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	// Test with non-string voice type (should use default)
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Hello",
		"voice": 123, // wrong type
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should use default voice (alloy)
	if result.IsError {
		t.Error("expected success with default voice")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "alloy") {
		t.Errorf("expected default voice 'alloy' in result, got: %s", content.Text)
	}
}

func TestHandleSpeak_NonStringTextType(t *testing.T) {
	srv, err := New()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Shutdown()

	// Test with non-string text type
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": 123, // wrong type
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for non-string text")
	}
}
