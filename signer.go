package goproxy

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	mathrand "math/rand"
	"net"
	"sort"
	"time"
	"github.com/zond/gotomic"
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
var certprivRSA crypto.Signer
var certprivECDSA crypto.Signer
var crtCache = gotomic.NewHash()

func signHost(ca tls.Certificate, hosts []string) (cert *tls.Certificate, err error) {
	
	value, exist := crtCache.Get(gotomic.StringKey(hosts[0]))
	if exist {
		cert = value.(*tls.Certificate)
		return
	}

	cert, err = generateCertificate(ca, hosts)

	for _, host := range hosts {
		crtCache.Put(gotomic.StringKey(host), cert)
	}

	return
}

func generateCertificate(ca tls.Certificate, hosts []string) (cert *tls.Certificate, err error) {
	var x509ca *x509.Certificate

	// Use the provided ca and not the global GoproxyCa for certificate generation.
	if x509ca, err = x509.ParseCertificate(ca.Certificate[0]); err != nil {
		return
	}
	start := time.Unix(0, 0)
	end, err := time.Parse("2006-01-02", "2049-12-31")
	if err != nil {
		panic(err)
	}

	serial := big.NewInt(mathrand.Int63())
	template := x509.Certificate{
		// TODO(elazar): instead of this ugly hack, just encode the certificate and hash the binary form.
		SerialNumber: serial,
		Issuer:       x509ca.Subject,
		Subject: pkix.Name{
			Organization: []string{"GoProxy untrusted MITM proxy Inc"},
		},
		NotBefore: start,
		NotAfter:  end,

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

	var certpriv crypto.Signer

	switch ca.PrivateKey.(type) {
	case *rsa.PrivateKey:
		certpriv = certprivRSA
	case *ecdsa.PrivateKey:
		certpriv = certprivECDSA
	default:
		err = fmt.Errorf("unsupported key type %T", ca.PrivateKey)
	}

	var derBytes []byte
	if derBytes, err = x509.CreateCertificate(rand.Reader, &template, x509ca, certpriv.Public(), ca.PrivateKey); err != nil {
		return
	}

	return &tls.Certificate{
		Certificate: [][]byte{derBytes, ca.Certificate[0]},
		PrivateKey:  certpriv,
	}, nil
}

func init() {
	certprivRSA, _ = rsa.GenerateKey(rand.Reader, 2048);
	certprivECDSA, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
