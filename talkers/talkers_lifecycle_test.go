package talkers

import (
	"sync"
	"testing"
)

func TestStopIsSafeBeforeRun(t *testing.T) {
	tracker := lifecycleTestTracker(nil)

	const callers = 16
	var stops sync.WaitGroup
	stops.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer stops.Done()
			tracker.Stop()
		}()
	}
	stops.Wait()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		tracker.Run()
	}()
	<-runDone
}

func TestStopWaitsForCaptureWorker(t *testing.T) {
	captureStarted := make(chan struct{})
	stopObserved := make(chan struct{})
	releaseCapture := make(chan struct{})
	var tracker *Tracker
	tracker = lifecycleTestTracker(func(string) {
		close(captureStarted)
		<-tracker.stopCh
		close(stopObserved)
		<-releaseCapture
	})

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		tracker.Run()
	}()
	<-captureStarted

	const callers = 16
	stopDone := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func() {
			tracker.Stop()
			stopDone <- struct{}{}
		}()
	}

	<-stopObserved
	select {
	case <-stopDone:
		t.Fatal("Stop returned while a capture worker was still active")
	default:
	}

	close(releaseCapture)
	for i := 0; i < callers; i++ {
		<-stopDone
	}
	<-runDone
}

func lifecycleTestTracker(captureFn func(string)) *Tracker {
	if captureFn == nil {
		captureFn = func(string) {
			panic("capture worker started unexpectedly")
		}
	}
	return &Tracker{
		devices:    []string{"capture-test"},
		lanDevices: map[string]bool{"capture-test": true},
		buckets:    make([]*bucket, 0, 1),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		captureFn:  captureFn,
	}
}
