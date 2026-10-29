package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// fakeJobs simulates the River client: it takes a while to stop and records how it was called.
type fakeJobs struct {
	mu            sync.Mutex
	stopped       bool
	hadTimeAtStop time.Duration
}

func (f *fakeJobs) Stop(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	f.mu.Lock()
	f.hadTimeAtStop = time.Until(deadline)
	f.mu.Unlock()

	time.Sleep(50 * time.Millisecond) // finish the running jobs
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
	return ctx.Err()
}

// A stuck request (for example, a stream that never ends) cannot keep the background jobs from
// stopping in order.
func TestShutdownStopsJobsEvenIfHTTPTimesOut(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release // stuck until the test ends
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	clientErr := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
		clientErr <- err
	}()
	<-entered

	jobs := &fakeJobs{}
	timeout := 300 * time.Millisecond
	start := time.Now()
	shutdown(timeout, srv, jobs, slog.New(slog.DiscardHandler))

	if d := time.Since(start); d > timeout+500*time.Millisecond {
		t.Errorf("the shutdown took %v (timeout %v)", d, timeout)
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if !jobs.stopped {
		t.Fatal("the jobs did not stop because the HTTP server ran over time")
	}
	if jobs.hadTimeAtStop < timeout/2 {
		t.Errorf("the jobs got only %v to stop", jobs.hadTimeAtStop)
	}
	// The stuck request was cut, instead of being left halfway.
	select {
	case err := <-clientErr:
		if err == nil {
			t.Error("the stuck request should have been cut")
		}
	case <-time.After(time.Second):
		t.Error("the stuck connection is still open after the shutdown")
	}
}
