package server

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

func TestNewWorkerPool(t *testing.T) {
	wp := NewWorkerPool(3, 100)

	if wp.workerCount != 3 {
		t.Errorf("expected workerCount 3, got %d", wp.workerCount)
	}
	if wp.queueSize != 100 {
		t.Errorf("expected queueSize 100, got %d", wp.queueSize)
	}
	if wp.ttsClient == nil {
		t.Error("expected ttsClient to be initialized")
	}
	if wp.audioPlayer == nil {
		t.Error("expected audioPlayer to be initialized")
	}
	if cap(wp.jobs) != 100 {
		t.Errorf("expected jobs channel capacity 100, got %d", cap(wp.jobs))
	}
}

func TestWorkerPool_Submit(t *testing.T) {
	wp := NewWorkerPool(2, 10)
	// Don't start workers - we just want to test submission

	job, err := wp.Submit("Hello, world!", tts.VoiceAlloy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if job == nil {
		t.Fatal("expected job to be returned")
	}
	if !strings.HasPrefix(job.ID, "job-") {
		t.Errorf("expected job ID to start with 'job-', got %s", job.ID)
	}
	if job.Text != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got %s", job.Text)
	}
	if job.Voice != tts.VoiceAlloy {
		t.Errorf("expected voice alloy, got %s", job.Voice)
	}
	if job.Status != "pending" {
		t.Errorf("expected status 'pending', got %s", job.Status)
	}
	if job.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestWorkerPool_Submit_QueueFull(t *testing.T) {
	// Create a pool with queue size 2
	wp := NewWorkerPool(1, 2)
	// Don't start workers so queue fills up

	// Fill the queue
	_, err1 := wp.Submit("Job 1", tts.VoiceAlloy)
	if err1 != nil {
		t.Fatalf("first job should succeed: %v", err1)
	}

	_, err2 := wp.Submit("Job 2", tts.VoiceEcho)
	if err2 != nil {
		t.Fatalf("second job should succeed: %v", err2)
	}

	// Third job should fail - queue is full
	job, err3 := wp.Submit("Job 3", tts.VoiceFable)
	if err3 == nil {
		t.Error("expected error when queue is full")
	}
	if !strings.Contains(err3.Error(), "queue is full") {
		t.Errorf("expected 'queue is full' error, got: %v", err3)
	}
	if job.Status != "failed" {
		t.Errorf("expected job status 'failed', got %s", job.Status)
	}
	if job.Error != "queue is full" {
		t.Errorf("expected job error 'queue is full', got %s", job.Error)
	}
}

func TestWorkerPool_JobHistory(t *testing.T) {
	wp := NewWorkerPool(1, 10)

	// Submit multiple jobs
	for i := 0; i < 5; i++ {
		_, err := wp.Submit("Test", tts.VoiceAlloy)
		if err != nil {
			t.Fatalf("job %d failed: %v", i, err)
		}
	}

	wp.historyMu.RLock()
	historyLen := len(wp.jobHistory)
	wp.historyMu.RUnlock()

	if historyLen != 5 {
		t.Errorf("expected 5 jobs in history, got %d", historyLen)
	}
}

func TestWorkerPool_JobHistoryLimit(t *testing.T) {
	wp := NewWorkerPool(1, 150)

	// Submit more than 100 jobs (history limit)
	for i := 0; i < 105; i++ {
		_, err := wp.Submit("Test", tts.VoiceAlloy)
		if err != nil {
			t.Fatalf("job %d failed: %v", i, err)
		}
	}

	wp.historyMu.RLock()
	historyLen := len(wp.jobHistory)
	wp.historyMu.RUnlock()

	if historyLen != 100 {
		t.Errorf("expected history to be capped at 100, got %d", historyLen)
	}
}

func TestWorkerPool_GetStatus(t *testing.T) {
	wp := NewWorkerPool(2, 50)

	// Submit a job without starting workers
	_, _ = wp.Submit("Test job", tts.VoiceNova)

	status := wp.GetStatus()

	if status.WorkerCount != 2 {
		t.Errorf("expected WorkerCount 2, got %d", status.WorkerCount)
	}
	if status.QueueSize != 50 {
		t.Errorf("expected QueueSize 50, got %d", status.QueueSize)
	}
	if status.QueuePending != 1 {
		t.Errorf("expected QueuePending 1, got %d", status.QueuePending)
	}
	if status.TotalProcessed != 0 {
		t.Errorf("expected TotalProcessed 0, got %d", status.TotalProcessed)
	}
	if status.TotalFailed != 0 {
		t.Errorf("expected TotalFailed 0, got %d", status.TotalFailed)
	}
	if status.IsPlaying {
		t.Error("expected IsPlaying to be false")
	}
	if len(status.RecentJobs) != 1 {
		t.Errorf("expected 1 recent job, got %d", len(status.RecentJobs))
	}
}

func TestWorkerPool_GetStatus_RecentJobsLimit(t *testing.T) {
	wp := NewWorkerPool(1, 50)

	// Submit 15 jobs
	for i := 0; i < 15; i++ {
		_, _ = wp.Submit("Test", tts.VoiceAlloy)
	}

	status := wp.GetStatus()

	// Should only return last 10 jobs
	if len(status.RecentJobs) != 10 {
		t.Errorf("expected 10 recent jobs, got %d", len(status.RecentJobs))
	}
}

func TestWorkerPool_StartStop(t *testing.T) {
	wp := NewWorkerPool(2, 10)

	wp.Start()

	// Give workers time to start
	time.Sleep(10 * time.Millisecond)

	// Stop should not deadlock
	done := make(chan struct{})
	go func() {
		wp.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Stop() timed out - possible deadlock")
	}
}

func TestJob_Fields(t *testing.T) {
	job := &Job{
		ID:        "test-123",
		Text:      "Hello",
		Voice:     tts.VoiceShimmer,
		CreatedAt: time.Now(),
		Status:    "pending",
		Error:     "",
	}

	if job.ID != "test-123" {
		t.Errorf("expected ID 'test-123', got %s", job.ID)
	}
	if job.Voice != tts.VoiceShimmer {
		t.Errorf("expected voice shimmer, got %s", job.Voice)
	}
}

func TestPoolStatus_JSON(t *testing.T) {
	status := PoolStatus{
		WorkerCount:    2,
		QueueSize:      50,
		QueuePending:   5,
		TotalProcessed: 100,
		TotalFailed:    3,
		IsPlaying:      true,
		RecentJobs:     nil,
	}

	if status.WorkerCount != 2 {
		t.Errorf("expected WorkerCount 2, got %d", status.WorkerCount)
	}
	if status.TotalProcessed != 100 {
		t.Errorf("expected TotalProcessed 100, got %d", status.TotalProcessed)
	}
}

func TestWorkerPool_ConcurrentSubmit(t *testing.T) {
	wp := NewWorkerPool(2, 100)

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	// Submit 50 jobs concurrently
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := wp.Submit("Concurrent test", tts.VoiceAlloy)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if successCount != 50 {
		t.Errorf("expected 50 successful submissions, got %d", successCount)
	}

	status := wp.GetStatus()
	if status.QueuePending != 50 {
		t.Errorf("expected 50 pending jobs, got %d", status.QueuePending)
	}
}
