package server

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ybouhjira/claude-code-tts/internal/audio"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// Job represents a TTS job in the queue
type Job struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Voice     tts.Voice `json:"voice"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"` // pending, processing, completed, failed
	Error     string    `json:"error,omitempty"`
	mu        sync.RWMutex
}

// WorkerPool manages TTS job processing
type WorkerPool struct {
	ttsClient   *tts.Client
	audioPlayer *audio.Player
	jobs        chan *Job
	jobHistory  []*Job
	historyMu   sync.RWMutex
	workerCount int
	queueSize   int
	processed   atomic.Int64
	failed      atomic.Int64
	wg          sync.WaitGroup
	shutdown    chan struct{}
}

// NewWorkerPool creates a new worker pool
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

// Start launches the worker goroutines
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workerCount; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	log.Printf("Started %d TTS workers with queue size %d", wp.workerCount, wp.queueSize)
}

// Stop gracefully shuts down the worker pool
func (wp *WorkerPool) Stop() {
	close(wp.shutdown)
	close(wp.jobs)
	wp.wg.Wait()
	log.Println("Worker pool stopped")
}

// worker processes jobs from the queue
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	for {
		select {
		case <-wp.shutdown:
			return
		case job, ok := <-wp.jobs:
			if !ok {
				return
			}
			wp.processJob(job)
		}
	}
}

// processJob handles a single TTS job
func (wp *WorkerPool) processJob(job *Job) {
	job.mu.Lock()
	job.Status = "processing"
	job.mu.Unlock()

	// Synthesize audio
	audioData, err := wp.ttsClient.Synthesize(job.Text, job.Voice)
	if err != nil {
		job.mu.Lock()
		job.Status = "failed"
		job.Error = err.Error()
		job.mu.Unlock()
		wp.failed.Add(1)
		log.Printf("Job %s failed: %v", job.ID, err)
		return
	}

	// Play audio (mutex protected - only one plays at a time)
	if err := wp.audioPlayer.Play(audioData); err != nil {
		job.mu.Lock()
		job.Status = "failed"
		job.Error = err.Error()
		job.mu.Unlock()
		wp.failed.Add(1)
		log.Printf("Job %s playback failed: %v", job.ID, err)
		return
	}

	job.mu.Lock()
	job.Status = "completed"
	job.mu.Unlock()
	wp.processed.Add(1)
	log.Printf("Job %s completed successfully", job.ID)
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

	// Track job history (keep last 100)
	wp.historyMu.Lock()
	wp.jobHistory = append(wp.jobHistory, job)
	if len(wp.jobHistory) > 100 {
		wp.jobHistory = wp.jobHistory[1:]
	}
	wp.historyMu.Unlock()

	select {
	case wp.jobs <- job:
		return job, nil
	default:
		job.Status = "failed"
		job.Error = "queue is full"
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
			ID:        job.ID,
			Text:      job.Text,
			Voice:     job.Voice,
			CreatedAt: job.CreatedAt,
			Status:    job.Status,
			Error:     job.Error,
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
		RecentJobs:     recentJobs,
	}
}
