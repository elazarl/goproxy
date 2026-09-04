package goproxy

import (
	"net/http"
	"testing"
)

// TestConnectResponseLineEchoesVersion verifies that CONNECT tunnel replies
// echo the client's HTTP version instead of a hardcoded HTTP/1.0 (issue #802).
func TestConnectResponseLineEchoesVersion(t *testing.T) {
	cases := []struct {
		name         string
		major, minor int
		text         string
		want         string
	}{
		{"http1.1 established", 1, 1, "Connection established", "HTTP/1.1 200 Connection established\r\n\r\n"},
		{"http1.0 established", 1, 0, "Connection established", "HTTP/1.0 200 Connection established\r\n\r\n"},
		{"http1.1 ok", 1, 1, "OK", "HTTP/1.1 200 OK\r\n\r\n"},
		{"unset falls back to 1.1", 0, 0, "OK", "HTTP/1.1 200 OK\r\n\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &http.Request{ProtoMajor: c.major, ProtoMinor: c.minor}
			if got := connectResponseLine(r, http.StatusOK, c.text); got != c.want {
				t.Errorf("connectResponseLine(%d.%d) = %q, want %q", c.major, c.minor, got, c.want)
			}
		})
	}
}
