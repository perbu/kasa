package main

import (
	"math"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

// retryTransport wraps an http.RoundTripper with exponential backoff retry
// for transient server errors (500, 502, 503, 429).
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
	debug      bool
}

// retryableStatusCodes are HTTP status codes that warrant a retry.
var retryableStatusCodes = map[int]bool{
	http.StatusTooManyRequests:     true, // 429
	http.StatusInternalServerError: true, // 500
	http.StatusBadGateway:          true, // 502
	http.StatusServiceUnavailable:  true, // 503
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			if t.debug {
				// klog is redirected to a file at startup; stderr belongs to
				// the interactive renderer while the REPL is running.
				klog.Infof("[retry] attempt %d/%d after %v", attempt+1, t.maxRetries+1, backoff)
			}

			timer := time.NewTimer(backoff)
			select {
			case <-req.Context().Done():
				timer.Stop()
				return nil, req.Context().Err()
			case <-timer.C:
			}
		}

		resp, err = base.RoundTrip(req)
		if err != nil {
			return nil, err // network errors are not retried
		}

		if !retryableStatusCodes[resp.StatusCode] {
			return resp, nil
		}

		if attempt < t.maxRetries {
			// Drain and close the body so the connection can be reused.
			resp.Body.Close()
		}
	}

	// Exhausted retries — return the last response as-is.
	return resp, nil
}
