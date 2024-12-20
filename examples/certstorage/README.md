# CertStorage

CertStorage example is important to improve the performance of an
HTTPS proxy server, which you can build using goproxy.
Without a `proxy.CertStore`, every HTTPS request will generate new TLS
certificates and this, repeated for hundreds of request, will destroy your CPU.

A lot of people opened issues in the projects complaining about this, because
they didn't use a certificates cache.
The cache implementation is up to you, maybe you can cache only the
most used hostnames, if you want to.
