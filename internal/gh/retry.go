package gh

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/go-github/v75/github"
)

// retryBackoffs is the fixed 250ms/1s/4s backoff for plain 5xx responses.
var retryBackoffs = [3]time.Duration{250 * time.Millisecond, time.Second, 4 * time.Second}

const maxRetrySleep = 60 * time.Second

// retryTransport wraps an http.RoundTripper with agentic-review's GitHub API
// retry policy: *github.RateLimitError and *github.AbuseRateLimitError retry
// up to 3 times, sleeping for the server-advised duration capped at 60s per
// sleep; any other 5xx retries up to 3 times with a 250ms/1s/4s backoff;
// every other status, including all other 4xx, returns immediately.
type retryTransport struct {
	base http.RoundTripper
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	var resp *http.Response
	var err error
	for attempt := 0; attempt <= len(retryBackoffs); attempt++ {
		if attempt > 0 && req.Body != nil {
			if req.GetBody == nil {
				return resp, err
			}
			body, gerr := req.GetBody()
			if gerr != nil {
				return nil, gerr
			}
			req.Body = body
		}
		resp, err = base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if attempt == len(retryBackoffs) {
			return resp, nil
		}
		wait, retryable := retryDelay(resp, attempt)
		if !retryable {
			return resp, nil
		}
		drainAndClose(resp)
		time.Sleep(wait)
	}
	return resp, err
}

// retryDelay reports whether resp should be retried and, if so, how long to
// wait first. It reconstructs the same typed error go-github's own Do()
// would produce so rate-limit detection matches exactly.
func retryDelay(resp *http.Response, attempt int) (time.Duration, bool) {
	if err := github.CheckResponse(resp); err != nil {
		var rl *github.RateLimitError
		var abuse *github.AbuseRateLimitError
		switch {
		case errors.As(err, &rl):
			return capDuration(time.Until(rl.Rate.Reset.Time), maxRetrySleep), true
		case errors.As(err, &abuse):
			if abuse.RetryAfter != nil {
				return capDuration(*abuse.RetryAfter, maxRetrySleep), true
			}
			return maxRetrySleep, true
		}
	}
	if resp.StatusCode >= 500 {
		return retryBackoffs[attempt], true
	}
	return 0, false
}

func capDuration(d, max time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > max {
		return max
	}
	return d
}

func drainAndClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
