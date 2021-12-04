package main

import (
	"crypto/tls"
	"sync"
)

type OptimizedCertStore struct {
	certs map[string]*tls.Certificate
	locks map[string]*sync.Mutex
	sync.Mutex
}

func NewOptimizedCertStore() *OptimizedCertStore {
	return &OptimizedCertStore{
		certs: map[string]*tls.Certificate{},
		locks: map[string]*sync.Mutex{},
	}
}

func (s *OptimizedCertStore) Fetch(host string, genCert func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	hostLock := s.hostLock(host)
	hostLock.Lock()
	defer hostLock.Unlock()

	cert, ok := s.certs[host]
	var err error
	if !ok {
		cert, err = genCert()
		if err != nil {
			return nil, err
		}
		s.certs[host] = cert
	}
	return cert, nil
}

func (s *OptimizedCertStore) hostLock(host string) *sync.Mutex {
	s.Lock()
	defer s.Unlock()

	lock, ok := s.locks[host]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[host] = lock
	}
	return lock
}
