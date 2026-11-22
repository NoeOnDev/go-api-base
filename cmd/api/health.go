package main

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/config"
)

// runHealthcheck asks the API running in this same machine for its /healthz, and fails if it does
// not answer 200. It is the health check of the container (see the Dockerfile): the image has no
// curl, and the binary knows where it listens (HTTP_ADDR).
func runHealthcheck() error {
	url, err := healthURL(config.Env("HTTP_ADDR"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return healthcheck(ctx, url)
}

// healthURL returns the address of /healthz for an HTTP_ADDR: this machine, unless the server
// listens on one address only (HTTP_ADDR=10.0.0.5:8080), which is then the one that answers.
func healthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(cmp.Or(addr, ":8080"))
	if err != nil {
		return "", fmt.Errorf("HTTP_ADDR: %w", err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
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
