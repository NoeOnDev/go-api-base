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

// fakeJobs simula el cliente de River: tarda un poco en detenerse y anota cómo lo llamaron.
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

	time.Sleep(50 * time.Millisecond) // terminar los trabajos en curso
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
	return ctx.Err()
}

// Una petición trabada (por ejemplo, un stream que no termina) no puede impedir que los
// trabajos en segundo plano se detengan en orden.
func TestShutdownStopsJobsEvenIfHTTPTimesOut(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release // trabada hasta que termina el test
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
		t.Errorf("el apagado tardó %v (timeout %v)", d, timeout)
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if !jobs.stopped {
		t.Fatal("los trabajos no se detuvieron porque el servidor HTTP se pasó del tiempo")
	}
	if jobs.hadTimeAtStop < timeout/2 {
		t.Errorf("los trabajos recibieron solo %v para detenerse", jobs.hadTimeAtStop)
	}
	// La petición trabada se cortó, en lugar de quedar a medias.
	select {
	case err := <-clientErr:
		if err == nil {
			t.Error("la petición trabada debería haberse cortado")
		}
	case <-time.After(time.Second):
		t.Error("la conexión trabada sigue abierta después del apagado")
	}
}
