package goproxy

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"runtime"
	"sort"
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

func signHost(ca tls.Certificate, hosts []string) (cert *tls.Certificate, err error) {
	// Use the provided CA for certificate generation.
	// Use already parsed Leaf certificate when present.
	x509ca := ca.Leaf
	if x509ca == nil {
		if x509ca, err = x509.ParseCertificate(ca.Certificate[0]); err != nil {
			return nil, err
		}
	}

	start := time.Unix(time.Now().Unix()-2592000, 0) // 2592000  = 30 day
	end := time.Unix(time.Now().Unix()+31536000, 0)  // 31536000 = 365 day

	// Always generate a positive int value
	// (Two complement is not enabled when the first bit is 0)
	generated := rand.Uint64()
	generated >>= 1

	template := x509.Certificate{
		SerialNumber: big.NewInt(int64(generated)),
		Issuer:       x509ca.Subject,
		Subject:      x509ca.Subject,
		NotBefore:    start,
		NotAfter:     end,

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

	var certpriv crypto.Signer
	switch ca.PrivateKey.(type) {
	case *rsa.PrivateKey:
		if certpriv, err = rsa.GenerateKey(&csprng, 2048); err != nil {
			return
		}
	case *ecdsa.PrivateKey:
		if certpriv, err = ecdsa.GenerateKey(elliptic.P256(), &csprng); err != nil {
			return
		}
	case ed25519.PrivateKey:
		if _, certpriv, err = ed25519.GenerateKey(&csprng); err != nil {
			return
		}
	default:
		err = fmt.Errorf("unsupported key type %T", ca.PrivateKey)
	}

	derBytes, err := x509.CreateCertificate(&csprng, &template, x509ca, certpriv.Public(), ca.PrivateKey)
	if err != nil {
		return nil, err
	}

	// Save an already parsed leaf certificate to use less CPU
	// when it will be used
	leafCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, err
	}

	certBytes := [][]byte{derBytes}
	certBytes = append(certBytes, ca.Certificate...)
	return &tls.Certificate{
		Certificate: certBytes,
		PrivateKey:  certpriv,
		Leaf:        leafCert,
	}, nil
}
