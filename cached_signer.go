package goproxy

import (
	"crypto/tls"
	"sort"
	"strings"
	"sync"
	"time"
)

type ExpiringCertMap struct {
	TTL  time.Duration

	data sync.Map
}

type expireEntry struct {
	ExpiresAt time.Time
	Value	  interface{}
}

func (t *ExpiringCertMap) Store(key string, val interface{}) {
	t.data.Store(key, expireEntry{
		ExpiresAt: time.Now().Add(t.TTL),
		Value: val,
	})
}

func (t *ExpiringCertMap) Load(key string) (val interface{}) {
	entry, ok := t.data.Load(key)
	if !ok { return nil }

	expireEntry := entry.(expireEntry)
	if expireEntry.ExpiresAt.After(time.Now()) { return nil }

	return expireEntry.Value
}

func newTTLMap(ttl time.Duration) (m ExpiringCertMap) {
	m.TTL = ttl

	go func() {
		for now := range time.Tick(time.Second) {
			m.data.Range(func(k, v interface{}) bool {
				if v.(expireEntry).ExpiresAt.After(now) {
					m.data.Delete(k)
				}
				return true
			})
		}
	}()

	return
}

type cachedSigner struct {
	cache     ExpiringCertMap
	semaphore chan struct{}
}

func newCachedSigner(ttl time.Duration) cachedSigner {
	return cachedSigner{cache: newTTLMap(ttl), semaphore: make(chan struct{}, 1)}
}

func (s *cachedSigner) signHost(ca tls.Certificate, hosts []string) (cert *tls.Certificate, err error) {
	sort.Strings(hosts)
	hostKey := strings.Join(hosts, ";")

	s.semaphore <- struct{}{}
	defer func() { <-s.semaphore }()

	if cachedCert := s.cache.Load(hostKey); cachedCert != nil {
		return cachedCert.(*tls.Certificate), nil
	}

	genCert, err := signHost(ca, hosts)
	if err != nil {
		return cert, err
	}

	s.cache.Store(hostKey, genCert)

	return genCert, nil
}
