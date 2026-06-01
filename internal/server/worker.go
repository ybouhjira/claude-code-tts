package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ybouhjira/claude-code-tts/internal/audio"
	"github.com/ybouhjira/claude-code-tts/internal/logging"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// Job represents a TTS job in the queue
type Job struct {
	ID           string    `json:"id"`
	Text         string    `json:"text"`
	Voice        tts.Voice `json:"voice"`
	ProviderName string    `json:"provider,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Status       string    `json:"status"` // pending, processing, completed, failed
	Error        string    `json:"error,omitempty"`
	mu           sync.RWMutex
}

// WorkerPool manages TTS job processing
type WorkerPool struct {
	// ttsClient is a legacy/test-only field retained for existing provider-agnostic
	// tests (worker_test.go).  Production code uses NewWorkerPoolWithRegistry and
	// the registry field instead.
	ttsClient   *tts.Client
	registry    *tts.Registry
	audioPlayer *audio.Player
	jobs        chan *Job
	jobHistory  []*Job
	historyMu   sync.RWMutex
	workerCount int
	queueSize   int
	processed   atomic.Int64
	failed      atomic.Int64
	paused      atomic.Bool
	wg          sync.WaitGroup
	shutdown    chan struct{}
}

// NewWorkerPool creates a new worker pool backed by the default OpenAI client.
// This is a legacy/test-only constructor retained for existing provider-agnostic
// tests in worker_test.go.  New production code should use NewWorkerPoolWithRegistry.
func NewWorkerPool(workerCount, queueSize int) *WorkerPool {
	return &WorkerPool{
		ttsClient:   tts.NewClient(),
		audioPlayer: audio.NewPlayer(),
		jobs:        make(chan *Job, queueSize),
		jobHistory:  make([]*Job, 0),
		workerCount: workerCount,
		queueSize:   queueSize,
		shutdown:    make(chan struct{}),
	}
}

// NewWorkerPoolWithRegistry creates a new worker pool that resolves providers
// from the supplied Registry.  This is the test seam used by server_provider_test.go
// to inject mock providers without touching real network or audio.
func NewWorkerPoolWithRegistry(workerCount, queueSize int, registry *tts.Registry) *WorkerPool {
	return &WorkerPool{
		registry:    registry,
		audioPlayer: audio.NewPlayer(),
		jobs:        make(chan *Job, queueSize),
		jobHistory:  make([]*Job, 0),
		workerCount: workerCount,
		queueSize:   queueSize,
		shutdown:    make(chan struct{}),
	}
}

// Start launches the worker goroutines
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workerCount; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	logging.Info("Started %d TTS workers with queue size %d", wp.workerCount, wp.queueSize)
}

// Stop gracefully shuts down the worker pool
func (wp *WorkerPool) Stop() {
	logging.Info("Stopping worker pool...")
	close(wp.shutdown)
	close(wp.jobs)
	wp.wg.Wait()
	logging.Info("Worker pool stopped (processed=%d, failed=%d)", wp.processed.Load(), wp.failed.Load())
}

// worker processes jobs from the queue
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()
	logging.Debug("Worker %d started", id)

	for {
		select {
		case <-wp.shutdown:
			logging.Debug("Worker %d shutting down", id)
			return
		case job, ok := <-wp.jobs:
			if !ok {
				logging.Debug("Worker %d: jobs channel closed", id)
				return
			}
			// Check if paused, wait until resumed
			for wp.paused.Load() {
				select {
				case <-wp.shutdown:
					logging.Debug("Worker %d: shutdown while paused", id)
					return
				case <-time.After(100 * time.Millisecond):
					// Continue checking pause status
				}
			}
			logging.Debug("Worker %d processing job %s", id, job.ID)
			wp.processJob(job)
		}
	}
}

// processJob handles a single TTS job
func (wp *WorkerPool) processJob(job *Job) {
	startTime := time.Now()
	logging.Info("Job %s: starting (provider=%s, voice=%s, text_len=%d)", job.ID, job.ProviderName, job.Voice, len(job.Text))

	job.mu.Lock()
	job.Status = "processing"
	job.mu.Unlock()

	// Resolve the synthesizer: prefer registry-based provider, fall back to ttsClient.
	var audioData []byte
	var err error
	if wp.registry != nil {
		provider, pErr := wp.registry.Get(job.ProviderName)
		if pErr != nil {
			job.mu.Lock()
			job.Status = "failed"
			job.Error = pErr.Error()
			job.mu.Unlock()
			wp.failed.Add(1)
			logging.Error("Job %s: unknown provider: %v", job.ID, pErr)
			return
		}
		logging.Debug("Job %s: calling %.64s TTS API...", job.ID, job.ProviderName)
		audioData, err = provider.Synthesize(job.Text, job.Voice)
	} else {
		logging.Debug("Job %s: calling OpenAI TTS API...", job.ID)
		audioData, err = wp.ttsClient.Synthesize(job.Text, job.Voice)
	}
	if err != nil {
		job.mu.Lock()
		job.Status = "failed"
		job.Error = err.Error()
		job.mu.Unlock()
		wp.failed.Add(1)
		logging.Error("Job %s: TTS synthesis failed after %v: %v", job.ID, time.Since(startTime), err)
		return
	}
	logging.Debug("Job %s: received %d bytes of audio", job.ID, len(audioData))

	// Play audio (mutex protected - only one plays at a time)
	logging.Debug("Job %s: starting audio playback...", job.ID)
	if err := wp.audioPlayer.Play(audioData); err != nil {
		job.mu.Lock()
		job.Status = "failed"
		job.Error = err.Error()
		job.mu.Unlock()
		wp.failed.Add(1)
		logging.Error("Job %s: playback failed after %v: %v", job.ID, time.Since(startTime), err)
		return
	}

	job.mu.Lock()
	job.Status = "completed"
	job.mu.Unlock()
	wp.processed.Add(1)
	logging.Info("Job %s: completed successfully in %v", job.ID, time.Since(startTime))
}

// SubmitWithProvider adds a new job to the queue using an explicit provider name.
// providerName is stored on the job so processJob can resolve the correct Provider
// from the registry.  Callers that do not need provider routing should use Submit.
func (wp *WorkerPool) SubmitWithProvider(text string, voice tts.Voice, providerName string) (*Job, error) {
	job := &Job{
		ID:           fmt.Sprintf("job-%d", time.Now().UnixNano()),
		Text:         text,
		Voice:        voice,
		ProviderName: providerName,
		CreatedAt:    time.Now(),
		Status:       "pending",
	}
	return wp.enqueue(job)
}

// Submit adds a new job to the queue
func (wp *WorkerPool) Submit(text string, voice tts.Voice) (*Job, error) {
	job := &Job{
		ID:        fmt.Sprintf("job-%d", time.Now().UnixNano()),
		Text:      text,
		Voice:     voice,
		CreatedAt: time.Now(),
		Status:    "pending",
	}
	return wp.enqueue(job)
}

// enqueue tracks the job in history and sends it to the jobs channel.
func (wp *WorkerPool) enqueue(job *Job) (*Job, error) {
	logging.Debug("Submit: created job %s", job.ID)

	// Track job history (keep last 100)
	wp.historyMu.Lock()
	wp.jobHistory = append(wp.jobHistory, job)
	if len(wp.jobHistory) > 100 {
		wp.jobHistory = wp.jobHistory[1:]
	}
	historyLen := len(wp.jobHistory)
	wp.historyMu.Unlock()

	logging.Debug("Submit: job history size = %d", historyLen)

	select {
	case wp.jobs <- job:
		logging.Debug("Submit: job %s queued (queue_pending=%d)", job.ID, len(wp.jobs))
		return job, nil
	default:
		job.Status = "failed"
		job.Error = "queue is full"
		logging.Warn("Submit: queue full, rejecting job %s", job.ID)
		return job, fmt.Errorf("job queue is full (size: %d)", wp.queueSize)
	}
}

// Status returns current worker pool statistics
type PoolStatus struct {
	WorkerCount    int    `json:"worker_count"`
	QueueSize      int    `json:"queue_size"`
	QueuePending   int    `json:"queue_pending"`
	TotalProcessed int64  `json:"total_processed"`
	TotalFailed    int64  `json:"total_failed"`
	IsPlaying      bool   `json:"is_playing"`
	IsPaused       bool   `json:"is_paused"`
	RecentJobs     []*Job `json:"recent_jobs,omitempty"`
}

// GetStatus returns the current pool status
func (wp *WorkerPool) GetStatus() PoolStatus {
	wp.historyMu.RLock()
	recentJobs := make([]*Job, 0)
	start := len(wp.jobHistory) - 10
	if start < 0 {
		start = 0
	}
	// Create deep copies to avoid race conditions with workers modifying jobs
	for _, job := range wp.jobHistory[start:] {
		job.mu.RLock()
		jobCopy := &Job{
			ID:           job.ID,
			Text:         job.Text,
			Voice:        job.Voice,
			ProviderName: job.ProviderName,
			CreatedAt:    job.CreatedAt,
			Status:       job.Status,
			Error:        job.Error,
		}
		job.mu.RUnlock()
		recentJobs = append(recentJobs, jobCopy)
	}
	wp.historyMu.RUnlock()

	return PoolStatus{
		WorkerCount:    wp.workerCount,
		QueueSize:      wp.queueSize,
		QueuePending:   len(wp.jobs),
		TotalProcessed: wp.processed.Load(),
		TotalFailed:    wp.failed.Load(),
		IsPlaying:      wp.audioPlayer.IsPlaying(),
		IsPaused:       wp.paused.Load(),
		RecentJobs:     recentJobs,
	}
}

// Pause pauses job processing (queued jobs will wait)
func (wp *WorkerPool) Pause() {
	wp.paused.Store(true)
	logging.Info("Worker pool paused")
}

// Resume resumes job processing
func (wp *WorkerPool) Resume() {
	wp.paused.Store(false)
	logging.Info("Worker pool resumed")
}

// Clear removes all pending jobs from the queue
func (wp *WorkerPool) Clear() int {
	cleared := 0
	for {
		select {
		case job := <-wp.jobs:
			job.mu.Lock()
			job.Status = "cancelled"
			job.Error = "queue cleared"
			job.mu.Unlock()
			cleared++
		default:
			logging.Info("Cleared %d pending jobs from queue", cleared)
			return cleared
		}
	}
}
