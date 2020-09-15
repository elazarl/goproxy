package goproxy

import (
	"crypto/sha1"
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

func hashSorted(lst []string) []byte {
	c := make([]string, len(lst))
	copy(c, lst)
	sort.Strings(c)
	h := sha1.New()
	for _, s := range c {
		h.Write([]byte(s + ","))
	}
	return h.Sum(nil)
}

func hashSortedBigInt(lst []string) *big.Int {
	rv := new(big.Int)
	rv.SetBytes(hashSorted(lst))
	return rv
}

var goproxySignerVersion = ":goroxy1"

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
	return cachedSigner{cache: newTTLMap(3600), semaphore: make(chan struct{}, 1)}
}

func (s *cachedSigner) signHost(ca tls.Certificate, hosts []string) (cert *tls.Certificate, err error) {
	if len(hosts) == 0 {
		return cert, errors.New("empty hosts given")
	}

	if len(ca.Certificate) == 0 {
		return cert, errors.New("no CA certificates given")
	}

	hostKey := strings.Join(hosts, "/")

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

func signHost(ca tls.Certificate, hosts []string) (cert *tls.Certificate, err error) {
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
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
			template.Subject.CommonName = h
		}
	}

	hash := hashSorted(append(hosts, goproxySignerVersion, ":"+runtime.Version()))
	var csprng CounterEncryptorRand
	if csprng, err = NewCounterEncryptorRandFromKey(ca.PrivateKey, hash); err != nil {
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
