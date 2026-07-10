package poller

import (
	"sync"
	"time"
)

// Runner manages a periodic poll loop with graceful shutdown.
type Runner struct {
	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{}
	started bool
	stopped bool
}

// Init allocates the stop channel. Call in your constructor.
func (r *Runner) Init() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopCh != nil {
		panic("poller: Runner.Init called more than once")
	}
	r.stopCh = make(chan struct{})
	r.doneCh = make(chan struct{})
}

// Run calls fn immediately, then every interval until Stop. Blocks.
func (r *Runner) Run(interval time.Duration, fn func()) {
	r.mu.Lock()
	if r.stopCh == nil {
		r.mu.Unlock()
		panic("poller: Runner.Init must be called before Run")
	}
	if r.started {
		r.mu.Unlock()
		panic("poller: Runner.Run called more than once")
	}
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.started = true
	stopCh := r.stopCh
	doneCh := r.doneCh
	r.mu.Unlock()

	defer close(doneCh)
	fn()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			fn()
		case <-stopCh:
			return
		}
	}
}

// Stop signals the loop to exit and waits for any active poll to finish.
// It is safe to call multiple times or concurrently.
func (r *Runner) Stop() {
	r.mu.Lock()
	if r.stopCh == nil {
		r.mu.Unlock()
		panic("poller: Runner.Init must be called before Stop")
	}
	if !r.stopped {
		close(r.stopCh)
	}
	r.stopped = true
	var doneCh <-chan struct{}
	if r.started {
		doneCh = r.doneCh
	}
	r.mu.Unlock()

	if doneCh != nil {
		<-doneCh
	}
}

// StopCh returns the channel for custom select logic.
func (r *Runner) StopCh() <-chan struct{} {
	return r.stopCh
}
