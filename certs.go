package goproxy

import "crypto/tls"

// server certificate used when interception HTTPS traffic
var SERVER_CERT = []byte(`-----BEGIN CERTIFICATE-----
MIICATCCAWoCCQD/6eUeFn3yRDANBgkqhkiG9w0BAQUFADBFMQswCQYDVQQGEwJB
VTETMBEGA1UECBMKU29tZS1TdGF0ZTEhMB8GA1UEChMYSW50ZXJuZXQgV2lkZ2l0
cyBQdHkgTHRkMB4XDTExMDYwMzEzMjEwMVoXDTEyMDYwMjEzMjEwMVowRTELMAkG
A1UEBhMCQVUxEzARBgNVBAgTClNvbWUtU3RhdGUxITAfBgNVBAoTGEludGVybmV0
IFdpZGdpdHMgUHR5IEx0ZDCBnzANBgkqhkiG9w0BAQEFAAOBjQAwgYkCgYEA0OwS
N2h83OXkzdv8azyvAwFsAWJZPyV8oLvL05OIUb+RgvzeA1FL3YXsRR1dIBLqD7H8
OmS1ZctpQ61N8dOKISTohGdkK0l3X1ZKNDlwCgHwYl0+GfX63kM7NoNeevA3/paT
Tej4d+MEZ/xKugCwNeKb1M9ULAB9fMGBrLP4D3MCAwEAATANBgkqhkiG9w0BAQUF
AAOBgQAITr5Ly40GBFfaYquy1IhhqbIzaTg8JaPnd7yBvxoez4U7D4SB8Gu90QdW
0t2fPdiNmLaUzHckPnSJURiUjXW1v7eEDCAN6Gxc2TVt/wc4xshgCiOL7XBqxmNA
c1kT5IqLS7CMqOnSBNCaTtQxba3E/xi8BcODJ8aeFw6AGU7O+A==
-----END CERTIFICATE-----`)
// server private key used when interception HTTPS traffic
var SERVER_KEY  = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIICWwIBAAKBgQDQ7BI3aHzc5eTN2/xrPK8DAWwBYlk/JXygu8vTk4hRv5GC/N4D
UUvdhexFHV0gEuoPsfw6ZLVly2lDrU3x04ohJOiEZ2QrSXdfVko0OXAKAfBiXT4Z
9freQzs2g1568Df+lpNN6Ph34wRn/Eq6ALA14pvUz1QsAH18wYGss/gPcwIDAQAB
AoGAF0mJCkYKTPEPHOcdbrKX62TYLhtRSVmbV6s3IAE826fXx1r6QDJqm2mXGWkZ
fT6+ejtjmvqowYz30cRagM8MgUuTRkDUhKMbAzSEO8uCEEoTLDOZpUUCSOg78WUH
jV04INJi6jpduPj5vjm81gcTvE0+jB8KLCQeu8PoVZKC5WkCQQD4x1rsjP4tfyCl
K/SXD2ou3Nlwf6wHH5CXXHbmzX3WnP0eFMJ5s3dKFlX4Kgl9eMTQC6zYsaqc22uq
lqOuEGetAkEA1vyL6okLsKQFp+vPAqZMw6P6gw4XEeG4MD0H+ruWxzLaIfqLq4w8
ZNQqWu5EyfHUfpNVFIR3ST+8ZkpW5be3nwJAIIwESzpO7qjZHoLXpwOvQp5GHD+3
w97PTd4c+CkeM3uqacsRflaKXrj5WlQ1laK9LPK6FEd6KLdUKKc4lscyqQJAemJy
VCWIHhqBjcJTqjJ5aLYkmg6fW3Kfo/ZaYIYBo4xzWPyEHjhK+Ss+oV0ak8uzKAs/
V9rA/VXnLmQLa+JWCQJAPxvmm5VLT0lFh6gYswvEJtUnJ++x1axbGlNxx+cg+vbT
QSD5/EcAsiDP5HgX2BQ8VubV+cruuuOew56wcLjS/Q==
-----END RSA PRIVATE KEY-----`)

var tlsCertificate,tlsCertificateError = tls.X509KeyPair(SERVER_CERT,SERVER_KEY)

func init() {
	if tlsCertificateError != nil {
		panic("Error parsing builtin keys"+tlsCertificateError.Error())
	}
}

var tlsClientSkipVerify = &tls.Config{InsecureSkipVerify: true}

var tlsConfig = &tls.Config{
	Certificates: []tls.Certificate{tlsCertificate},
	InsecureSkipVerify: true,
}
