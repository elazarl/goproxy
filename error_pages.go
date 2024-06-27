package goproxy

import (
	"net"
	"net/http"
	"strings"
)

type ErrorPages struct {
	ErrorPageConnect []byte
	ErrorPageDNS     []byte
	ErrorPageGeneral []byte
}

// WriteErrorPage takes an error from a call to ProxyCtx.RoundTrip(), and writes the
// appropriate error page to the http.ResponseWriter depending on the type of error.
func (e *ErrorPages) WriteErrorPage(err error, host string, w http.ResponseWriter) {
	// If any of ErrorPageConnect, ErrorPageDNS, or ErrorPageGeneral are empty,
	// return and do nothing.
	if !e.Enabled() {
		return
	}

	// Determine the error page to display based on the contents of
	var status int
	var body []byte
	switch err.(type) {
	case *net.OpError:
		status = http.StatusBadGateway
		bodyTemplate := string(e.ErrorPageConnect)
		body = []byte(strings.ReplaceAll(bodyTemplate, "%H", host))
	case *net.DNSError:
		status = http.StatusBadRequest
		bodyTemplate := string(e.ErrorPageDNS)
		body = []byte(strings.ReplaceAll(bodyTemplate, "%H", host))
	default:
		status = http.StatusInternalServerError
		bodyTemplate := string(e.ErrorPageGeneral)
		body = []byte(strings.ReplaceAll(bodyTemplate, "%H", host))
	}

	// Write the error page to the client
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(status)
	w.Write(body)
}

// Enabled returns true if all of the error pages are set.
func (e *ErrorPages) Enabled() bool {
	return e.ErrorPageConnect != nil && e.ErrorPageDNS != nil && e.ErrorPageGeneral != nil
}
