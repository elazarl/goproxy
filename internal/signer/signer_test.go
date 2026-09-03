package signer_test

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/elazarl/goproxy/internal/signer"
	"github.com/elazarl/goproxy/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signedHosts are the names every certificate signed in this file is valid for.
var signedHosts = []string{"example.com", "1.1.1.1", "localhost"}

// signHost signs signedHosts with ca and returns the certificate with its
// leaf already parsed.
func signHost(t *testing.T, ca tls.Certificate) *tls.Certificate {
	t.Helper()
	cert, err := signer.SignHost(ca, signedHosts)
	require.NoError(t, err)
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	return cert
}

// assertSignedCertIsValid checks that a certificate signed by ca verifies
// against ca with the x509 package alone, without any TLS handshake.
func assertSignedCertIsValid(t *testing.T, ca tls.Certificate) {
	t.Helper()
	cert := signHost(t, ca)

	require.NoError(t, cert.Leaf.VerifyHostname("example.com"))
	require.NoError(t, cert.Leaf.CheckSignatureFrom(ca.Leaf))

	certPool := x509.NewCertPool()
	certPool.AddCert(ca.Leaf)
	_, err := cert.Leaf.Verify(x509.VerifyOptions{
		DNSName: "example.com",
		Roots:   certPool,
	})
	require.NoError(t, err)
}

// assertSignedCertServesTLS checks that a certificate signed by ca is accepted
// by the Go TLS client when ca is the only trusted root.
func assertSignedCertServesTLS(t *testing.T, ca tls.Certificate) {
	t.Helper()
	cert := signHost(t, ca)

	const expected = "key verifies with Go"
	server := httptest.NewUnstartedServer(testutil.ConstantHandler(expected))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{*cert, ca},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	certPool := x509.NewCertPool()
	certPool.AddCert(ca.Leaf)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: certPool},
	}

	// The signed certificate covers "localhost" but not "127.0.0.1".
	asLocalhost := strings.ReplaceAll(server.URL, "127.0.0.1", "localhost")
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, asLocalhost, nil)
	require.NoError(t, err)
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, expected, string(body))
}

func TestSignerRsaTls(t *testing.T) {
	assertSignedCertServesTLS(t, goproxy.GoproxyCa)
}

func TestSignerRsaX509(t *testing.T) {
	assertSignedCertIsValid(t, goproxy.GoproxyCa)
}

func TestSignerEcdsaTls(t *testing.T) {
	assertSignedCertServesTLS(t, ecdsaCA)
}

func TestSignerEcdsaX509(t *testing.T) {
	assertSignedCertIsValid(t, ecdsaCA)
}

func BenchmarkSignRsa(b *testing.B) {
	for range b.N {
		_, err := signer.SignHost(goproxy.GoproxyCa, signedHosts)
		require.NoError(b, err)
	}
}

func BenchmarkSignEcdsa(b *testing.B) {
	for range b.N {
		_, err := signer.SignHost(ecdsaCA, signedHosts)
		require.NoError(b, err)
	}
}

//
// Elliptic curve certificate and key for testing
//

var ecdsaCACert = []byte(`-----BEGIN CERTIFICATE-----
MIICGDCCAb8CFEkSgqYhlT0+Yyr9anQNJgtclTL0MAoGCCqGSM49BAMDMIGOMQsw
CQYDVQQGEwJJTDEPMA0GA1UECAwGQ2VudGVyMQwwCgYDVQQHDANMb2QxEDAOBgNV
BAoMB0dvUHJveHkxEDAOBgNVBAsMB0dvUHJveHkxGjAYBgNVBAMMEWdvcHJveHku
Z2l0aHViLmlvMSAwHgYJKoZIhvcNAQkBFhFlbGF6YXJsQGdtYWlsLmNvbTAeFw0x
OTA1MDcxMTUwMThaFw0zOTA1MDIxMTUwMThaMIGOMQswCQYDVQQGEwJJTDEPMA0G
A1UECAwGQ2VudGVyMQwwCgYDVQQHDANMb2QxEDAOBgNVBAoMB0dvUHJveHkxEDAO
BgNVBAsMB0dvUHJveHkxGjAYBgNVBAMMEWdvcHJveHkuZ2l0aHViLmlvMSAwHgYJ
KoZIhvcNAQkBFhFlbGF6YXJsQGdtYWlsLmNvbTBZMBMGByqGSM49AgEGCCqGSM49
AwEHA0IABDlH4YrdukPFAjbO8x+gR9F8ID7eCU8Orhba/MIblSRrRVedpj08lK+2
svyoAcrcDsynClO9aQtsC9ivZ+Pmr3MwCgYIKoZIzj0EAwMDRwAwRAIgGRSSJVSE
1b1KVU0+w+SRtnR5Wb7jkwnaDNxQ3c3FXoICIBJV/l1hFM7mbd68Oi5zLq/4ZsrL
98Bb3nddk2xys6a9
-----END CERTIFICATE-----`)

var ecdsaCAKey = []byte(`-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgEsc8m+2aZfagnesg
qMgXe8ph4LtVu2VOUYhHttuEDsChRANCAAQ5R+GK3bpDxQI2zvMfoEfRfCA+3glP
Dq4W2vzCG5Uka0VXnaY9PJSvtrL8qAHK3A7MpwpTvWkLbAvYr2fj5q9z
-----END PRIVATE KEY-----`)

var ecdsaCA = mustParseEcdsaCA()

func mustParseEcdsaCA() tls.Certificate {
	ca, err := tls.X509KeyPair(ecdsaCACert, ecdsaCAKey)
	if err != nil {
		panic("error parsing ecdsa CA: " + err.Error())
	}
	ca.Leaf, err = x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		panic("error parsing ecdsa CA leaf: " + err.Error())
	}
	return ca
}
