package ai

import (
	"errors"
	"testing"
)

func TestIsTransientError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("Our servers are currently overloaded. Please try again later."), true},
		{errors.New("API Codex error (HTTP 503): overloaded"), true},
		{errors.New("rate_limit_exceeded: please slow down"), true},
		{errors.New("HTTP 429 Too Many Requests"), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("Invalid API Key"), false},
		{errors.New("model not found"), false},
		{nil, false},
	}

	for _, tc := range cases {
		got := isTransientError(tc.err)
		if got != tc.want {
			t.Errorf("isTransientError(%v) = %v; want %v", tc.err, got, tc.want)
		}
	}
}
