package speedtest

import (
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"bandwidth-monitor/httputil"
)

// Result holds the outcome of a single speed test run.
type Result struct {
	Server       string  `json:"server"`
	DownloadMbps float64 `json:"download_mbps"`
	UploadMbps   float64 `json:"upload_mbps"`
	// DownloadSingleMbps/UploadSingleMbps are measured with a single TCP
	// connection (parallelism=1), run right before the full multi-stream
	// test. Comparing the two shows whether the link's aggregate capacity
	// is being limited by a per-connection cap (ISP per-flow shaping,
	// single-stream TCP window/RTT limits, etc) rather than the link
	// itself. Omitted from JSON if a value couldn't be measured.
	DownloadSingleMbps float64 `json:"download_single_mbps,omitempty"`
	UploadSingleMbps   float64 `json:"upload_single_mbps,omitempty"`
	PingMs             float64 `json:"ping_ms"`
	JitterMs           float64 `json:"jitter_ms"`
	Timestamp          int64   `json:"timestamp"`
}

// Progress is sent over SSE while a test is running.
type Progress struct {
	Phase   string  `json:"phase"`
	Percent float64 `json:"percent"`
	Value   float64 `json:"value"`
	Result  *Result `json:"result,omitempty"`
}

// Tester manages speed tests against a configured server.
type Tester struct {
	server string

	mu       sync.Mutex
	running  bool
	results  []Result
	progress chan Progress
}

// New creates a Tester for the given server URL (no trailing slash).
func New(server string) *Tester {
	return &Tester{
		server:  server,
		results: make([]Result, 0),
	}
}

// IsRunning returns whether a test is currently in progress.
func (t *Tester) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// GetResults returns a copy of all stored results (newest first).
func (t *Tester) GetResults() []Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Result, len(t.results))
	copy(out, t.results)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Run starts a speed test in the background. Returns a channel that receives
// progress updates. If a test is already running, returns nil.
func (t *Tester) Run() <-chan Progress {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return nil
	}
	t.running = true
	ch := make(chan Progress, 32)
	t.progress = ch
	t.mu.Unlock()

	go t.run(ch)
	return ch
}

func (t *Tester) run(ch chan<- Progress) {
	defer func() {
		t.mu.Lock()
		t.running = false
		t.progress = nil
		t.mu.Unlock()
		close(ch)
	}()

	server := t.server
	log.Printf("speedtest: starting test against %s", server)

	ch <- Progress{Phase: "ping", Percent: 0, Value: 0}
	pingMs, jitterMs, err := measurePing(server, 20)
	if err != nil {
		log.Printf("speedtest: ping failed: %v", err)
		ch <- Progress{Phase: "error", Value: 0}
		return
	}
	ch <- Progress{Phase: "ping", Percent: 100, Value: pingMs}
	log.Printf("speedtest: ping=%.1fms jitter=%.1fms", pingMs, jitterMs)

	// Single-stream sub-tests run first and briefly, purely as a point of
	// comparison against the full multi-stream result below — a large gap
	// between the two usually means something (ISP per-flow shaping, a
	// slow-start-limited high-RTT path, etc) is capping a single TCP
	// connection well below the link's real aggregate capacity. Failures
	// here are non-fatal: they're a bonus diagnostic, not the primary result.
	ch <- Progress{Phase: "download-single", Percent: 0, Value: 0}
	dlSingleMbps, err := measureDownload(server, 1, singleStreamDuration, "download-single", ch)
	if err != nil {
		log.Printf("speedtest: single-stream download measurement failed (non-fatal): %v", err)
		dlSingleMbps = 0
	}

	ch <- Progress{Phase: "download", Percent: 0, Value: 0}
	dlMbps, err := measureDownload(server, downloadParallelism, downloadDuration, "download", ch)
	if err != nil {
		log.Printf("speedtest: download failed: %v", err)
		ch <- Progress{Phase: "error", Value: 0}
		return
	}
	log.Printf("speedtest: download=%.1f Mbps (single-stream=%.1f Mbps)", dlMbps, dlSingleMbps)

	ch <- Progress{Phase: "upload-single", Percent: 0, Value: 0}
	ulSingleMbps, err := measureUpload(server, 1, singleStreamDuration, "upload-single", ch)
	if err != nil {
		log.Printf("speedtest: single-stream upload measurement failed (non-fatal): %v", err)
		ulSingleMbps = 0
	}

	ch <- Progress{Phase: "upload", Percent: 0, Value: 0}
	ulMbps, err := measureUpload(server, uploadParallelism, uploadDuration, "upload", ch)
	if err != nil {
		log.Printf("speedtest: upload failed: %v", err)
		ch <- Progress{Phase: "error", Value: 0}
		return
	}
	log.Printf("speedtest: upload=%.1f Mbps (single-stream=%.1f Mbps)", ulMbps, ulSingleMbps)

	result := Result{
		Server:             server,
		DownloadMbps:       dlMbps,
		UploadMbps:         ulMbps,
		DownloadSingleMbps: dlSingleMbps,
		UploadSingleMbps:   ulSingleMbps,
		PingMs:             pingMs,
		JitterMs:           jitterMs,
		Timestamp:          time.Now().UnixMilli(),
	}

	t.mu.Lock()
	t.results = append(t.results, result)
	if len(t.results) > 50 {
		t.results = t.results[len(t.results)-50:]
	}
	t.mu.Unlock()

	ch <- Progress{Phase: "done", Percent: 100, Result: &result}
	log.Printf("speedtest: completed — DL %.1f Mbps, UL %.1f Mbps, Ping %.1f ms",
		dlMbps, ulMbps, pingMs)
}

func measurePing(server string, samples int) (avgMs, jitterMs float64, err error) {
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: httputil.WrapTransport(nil),
	}

	var pings []float64
	for i := 0; i < samples; i++ {
		start := time.Now()
		req, e := http.NewRequest("HEAD", server+"/downloading", nil)
		if e != nil {
			return 0, 0, fmt.Errorf("creating request: %w", e)
		}
		resp, e := client.Do(req)
		if e != nil {
			continue
		}
		resp.Body.Close()
		rtt := float64(time.Since(start).Microseconds()) / 1000.0
		pings = append(pings, rtt)
	}

	if len(pings) < 2 {
		return 0, 0, fmt.Errorf("not enough ping responses (%d/%d)", len(pings), samples)
	}

	sort.Float64s(pings)
	qStart := len(pings) / 4
	qEnd := len(pings) - qStart
	if qEnd <= qStart {
		qStart = 0
		qEnd = len(pings)
	}

	var sum float64
	for i := qStart; i < qEnd; i++ {
		sum += pings[i]
	}
	avgMs = sum / float64(qEnd-qStart)

	var jitterSum float64
	count := 0
	for i := 1; i < len(pings); i++ {
		diff := pings[i] - pings[i-1]
		if diff < 0 {
			diff = -diff
		}
		jitterSum += diff
		count++
	}
	if count > 0 {
		jitterMs = jitterSum / float64(count)
	}

	return avgMs, jitterMs, nil
}

// measureThroughput runs parallelism goroutines calling workerFn for the
// given duration, reporting progress on ch under the given phase name.
// Returns the measured throughput in Mbps.
func measureThroughput(phase string, duration time.Duration, parallelism int, workerFn func(deadline time.Time, counter *int64, mu *sync.Mutex), ch chan<- Progress) (float64, error) {
	var totalBytes int64
	var mu sync.Mutex
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerFn(deadline, &totalBytes, &mu)
		}()
	}

	startTime := time.Now()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

loop:
	for {
		select {
		case <-done:
			break loop
		case <-ticker.C:
			elapsed := time.Since(startTime).Seconds()
			pct := (elapsed / duration.Seconds()) * 100
			if pct > 100 {
				pct = 100
			}
			mu.Lock()
			b := totalBytes
			mu.Unlock()
			mbps := (float64(b) * 8) / (elapsed * 1e6)
			ch <- Progress{Phase: phase, Percent: pct, Value: mbps}
		}
	}

	mu.Lock()
	b := totalBytes
	mu.Unlock()

	elapsed := time.Since(startTime).Seconds()
	if elapsed == 0 || b == 0 {
		return 0, fmt.Errorf("no data transferred during %s", phase)
	}

	mbps := (float64(b) * 8) / (elapsed * 1e6)
	ch <- Progress{Phase: phase, Percent: 100, Value: mbps}
	return mbps, nil
}

// newSpeedTransport builds a dedicated HTTP/1.1-only Transport for a single
// speedtest worker connection.
//
// Go's http.DefaultTransport auto-negotiates HTTP/2 over TLS, and multiple
// http.Client values that fall back to it (nil Transport.Base) all share
// that one global Transport/connection pool. For an HTTP/2 origin, that
// means every "parallel" worker ends up multiplexed over a single TCP
// connection with a single congestion-control window — throughput is then
// capped at whatever one TCP stream can sustain, which is far below the
// aggregate a real multi-connection speed test needs to saturate a fast
// link. Forcing HTTP/1.1 with a private Transport per worker guarantees
// each one gets its own independent TCP connection (and congestion
// window), which is how genuine multi-stream throughput tests reach the
// link's actual capacity.
func newSpeedTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConnsPerHost: 1,
		DisableCompression:  true,
		// A non-nil (even empty) TLSNextProto map disables the automatic
		// HTTP/2 upgrade, keeping this connection on HTTP/1.1.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
}

// Speed test timing/parallelism knobs.
const (
	downloadDuration     = 15 * time.Second
	downloadParallelism  = 6
	uploadDuration       = 15 * time.Second
	uploadParallelism    = 6
	singleStreamDuration = 4 * time.Second
	uploadChunkSize      = 4 * 1024 * 1024
)

func measureDownload(server string, parallelism int, duration time.Duration, phase string, ch chan<- Progress) (float64, error) {
	workerFn := func(deadline time.Time, counter *int64, mu *sync.Mutex) {
		client := &http.Client{
			Timeout:   duration + 5*time.Second,
			Transport: httputil.WrapTransport(newSpeedTransport()),
		}
		buf := make([]byte, 256*1024)
		for time.Now().Before(deadline) {
			resp, e := client.Get(server + "/downloading")
			if e != nil {
				return
			}
			for time.Now().Before(deadline) {
				n, e := resp.Body.Read(buf)
				if n > 0 {
					mu.Lock()
					*counter += int64(n)
					mu.Unlock()
				}
				if e != nil {
					break
				}
			}
			resp.Body.Close()
		}
	}

	return measureThroughput(phase, duration, parallelism, workerFn, ch)
}

func measureUpload(server string, parallelism int, duration time.Duration, phase string, ch chan<- Progress) (float64, error) {
	workerFn := func(deadline time.Time, counter *int64, mu *sync.Mutex) {
		client := &http.Client{
			Timeout:   duration + 5*time.Second,
			Transport: httputil.WrapTransport(newSpeedTransport()),
		}
		data := make([]byte, uploadChunkSize)
		rand.Read(data)

		for time.Now().Before(deadline) {
			reader := &countingReader{
				data:    data,
				mu:      mu,
				counter: counter,
			}
			req, e := http.NewRequest("POST", server+"/upload", reader)
			if e != nil {
				return
			}
			req.ContentLength = int64(uploadChunkSize)
			req.Header.Set("Content-Type", "application/octet-stream")

			resp, e := client.Do(req)
			if e != nil {
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}

	return measureThroughput(phase, duration, parallelism, workerFn, ch)
}

type countingReader struct {
	data    []byte
	offset  int
	mu      *sync.Mutex
	counter *int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	r.mu.Lock()
	*r.counter += int64(n)
	r.mu.Unlock()
	return n, nil
}
