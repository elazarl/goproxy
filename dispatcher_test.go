package goproxy_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
)

func TestIsLocalHost(t *testing.T) {
	hosts := []string{
		"localhost",
		"127.0.0.1",
		"127.0.0.7",
		"::ffff:127.0.0.1",
		"::ffff:127.0.0.7",
		"::1",
		"0:0:0:0:0:0:0:1",
	}
	ports := []string{
		"",
		"80",
		"443",
	}

	for _, host := range hosts {
		for _, port := range ports {
			if port == "" && strings.HasPrefix(host, "::ffff:") {
				continue
			}

			addr := host
			if port != "" {
				addr = net.JoinHostPort(host, port)
			}
			t.Run(addr, func(t *testing.T) {
				req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				if !goproxy.IsLocalHost(req, nil) {
					t.Fatal("expected true")
				}
			})
		}
	}
}
