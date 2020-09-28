package goproxy

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"math/rand"
	"net"
	"runtime"
	"strings"
	"time"
)

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
			Organization: []string{"proxy"},
		},

		NotBefore: start,
		NotAfter:  end,

		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

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
