package main

import (
	"crypto/tls"
	"sync"
)

type SimpleCertStorage struct {
	certs sync.Map
}

func NewSimpleCertStorage() *SimpleCertStorage {
	return &SimpleCertStorage{}
}

func (tcs *SimpleCertStorage) Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	var cert tls.Certificate
	icert, ok := tcs.certs.Load(hostname)
	if ok {
		cert = icert.(tls.Certificate)
	} else {
		certp, err := gen()
		if err != nil {
			return nil, err
		}
		// store as concrete implementation
		cert = *certp
		tcs.certs.Store(hostname, cert)
	}
	return &cert, nil
}
