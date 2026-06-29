# Simple HTTP Proxy

This example contains a base HTTP proxy server that listens on port :8080 and targets an HTTP/2 server.
It only handles explicit CONNECT requests.

Start it in one shell:

```sh
go build
http2-mitm -v
```

Fetch a test backend server using the proxy:
```sh
curl -x http://127.0.0.1:8080 -k https://localhost:40755
```

Result:
```
mitm response
```
