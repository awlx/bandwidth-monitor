package poller

import (
	"sync"
	"testing"
	"time"
)

func TestStopIsSafeBeforeRun(t *testing.T) {
	var runner Runner
	runner.Init()

	const callers = 16
	var stops sync.WaitGroup
	stops.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer stops.Done()
			runner.Stop()
		}()
	}
	stops.Wait()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		runner.Run(time.Hour, func() {
			t.Error("poll ran after Runner was stopped")
		})
	}()
	<-runDone
}

func TestStopWaitsForActivePoll(t *testing.T) {
	var runner Runner
	runner.Init()

	pollStarted := make(chan struct{})
	releasePoll := make(chan struct{})
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		runner.Run(time.Hour, func() {
			close(pollStarted)
			<-releasePoll
		})
	}()
	<-pollStarted

	const callers = 16
	stopDone := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func() {
			runner.Stop()
			stopDone <- struct{}{}
		}()
	}

	<-runner.stopCh
	select {
	case <-stopDone:
		t.Fatal("Stop returned while a poll was still active")
	default:
	}

	close(releasePoll)
	for i := 0; i < callers; i++ {
		<-stopDone
	}
	<-runDone
}
