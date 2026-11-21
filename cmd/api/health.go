package main

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// runHealthcheck asks the API running in this same machine for its /healthz, and fails if it does
// not answer 200. It is the health check of the container (see the Dockerfile): the image has no
// curl, and the binary knows which port it listens on (HTTP_ADDR).
func runHealthcheck() error {
	_, port, err := net.SplitHostPort(cmp.Or(os.Getenv("HTTP_ADDR"), ":8080"))
	if err != nil {
		return fmt.Errorf("HTTP_ADDR: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return healthcheck(ctx, "http://"+net.JoinHostPort("127.0.0.1", port)+"/healthz")
}

func healthcheck(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	return nil
}
