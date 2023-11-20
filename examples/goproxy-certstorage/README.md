# In-memory storage for temporary certificates

By default goproxy will generate TLS certificate for every request. Generating
certificates is computationally expensive, so this leads to performance issues
when there are many requests to the same host.

Certificates storage allows the reuse of certificates.

`SimpleCertStorage` - simple implementation, easy to read.

`OptimizedCertStore` - has per-host locks to avoid situation, when multiple
concurrent requests to a host without ready to use certificate will generate
the same certificate multiple times.
