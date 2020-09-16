package goproxy

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"math/rand"
	"net"
	"runtime"
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

func newCachedSigner() cachedSigner {
	return cachedSigner{cache: newTTLMap(time.Hour), semaphore: make(chan struct{}, 1)}
}

func (s *cachedSigner) signHost(ca tls.Certificate, hosts []string) (cert *tls.Certificate, err error) {
	if len(ca.Certificate) == 0 {
		return cert, errors.New("no CA certificates given")
	}

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

func signHost(ca tls.Certificate, hosts  []string) (cert *tls.Certificate, err error) {
	var x509ca *x509.Certificate
	if x509ca, err = x509.ParseCertificate(ca.Certificate[0]); err != nil {
		return
	}
	start := time.Now()
	end := start.AddDate(10, 0, 0)

	serial := big.NewInt(rand.Int63())
	template := x509.Certificate{
		SerialNumber: serial,
		Issuer:       x509ca.Subject,
		Subject: pkix.Name{
			Organization: []string{"http proxy inc."},
		},
		NotBefore:             start,
		NotAfter:              end,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	for _, host := range hosts {
		host = stripPort(host)

		if template.Subject.CommonName == "" {
			template.Subject.CommonName = host
		}

		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
			continue
		}

		template.DNSNames = append(template.DNSNames, host)
		template.Subject.CommonName = host
	}

	hash := strings.Join(hosts,",") + ":" + runtime.Version()
	var csprng CounterEncryptorRand
	if csprng, err = NewCounterEncryptorRandFromKey(ca.PrivateKey, []byte(hash)); err != nil {
		return
	}

	var derBytes []byte
	if derBytes, err = x509.CreateCertificate(&csprng, &template, x509ca, x509ca.PublicKey, ca.PrivateKey); err != nil {
		return
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{derBytes, ca.Certificate[0]},
		PrivateKey:  ca.PrivateKey,
	}

	return tlsCert, nil
}

func init() {
	// Avoid deterministic random numbers
	rand.Seed(time.Now().UnixNano())
}
