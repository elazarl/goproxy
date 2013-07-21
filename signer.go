package goproxy

import (
	"bytes"
	"math/big"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"sort"
	"time"
)

func hashSorted(lst []string) *big.Int {
	c := make([]string, len(lst))
	copy(c,lst)
	sort.Strings(c)
	h := sha1.New()
	for _, s := range c {
		h.Write([]byte(s + ","))
	}
	rv := new(big.Int)
	rv.SetBytes(h.Sum(nil))
	return rv
}

func signHost(ca tls.Certificate, hosts []string) (cert tls.Certificate, err error) {
	x509ca, err := x509.ParseCertificate(GoproxyCa.Certificate[0])
	if err != nil {
		return tls.Certificate{}, err
	}
	// TODO(elazar): the hops I'm going through to use X509KeyPair method are ridiculous, and on the verge of absurd,
	// yet, CPU is cheap nowadays, and we're going to cache it anyhow, so whatever...
	pemCert, pemKey, err := signHostX509(x509ca, ca.PrivateKey, hosts)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(pemCert, pemKey)
}

func signHostX509(ca *x509.Certificate, capriv interface{}, hosts []string) (pemCert []byte, pemKey []byte, err error) {
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: hashSorted(hosts),
		Issuer: ca.Subject,
		Subject: pkix.Name{
			Organization: []string{"GoProxy untrusted MITM proxy Inc"},
		},
		NotBefore: time.Now(),
		NotAfter:  now.Add(365*24*time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}
	certpriv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, nil, err
	}
	pemKeyBuf := new(bytes.Buffer)
	pem.Encode(pemKeyBuf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certpriv)})
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, ca, &certpriv.PublicKey, capriv)
	if err != nil {
		return nil, nil, err
	}
	pemCertBuf := new(bytes.Buffer)
	pem.Encode(pemCertBuf, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	return pemCertBuf.Bytes(), pemKeyBuf.Bytes(), nil
}
