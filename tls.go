package goproxy

import (
	"crypto/tls"
)

var tlsClientSkipVerify = &tls.Config{
	InsecureSkipVerify:       true,
	Renegotiation:            tls.RenegotiateOnceAsClient,
	SessionTicketsDisabled:   true,
	PreferServerCipherSuites: true,
}

var defaultTLSConfig = &tls.Config{
	InsecureSkipVerify:       true,
	Renegotiation:            tls.RenegotiateOnceAsClient,
	SessionTicketsDisabled:   true,
	PreferServerCipherSuites: true,
	NextProtos:               []string{"http/1.1", "http/1.0"},
}
