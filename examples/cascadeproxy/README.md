# CascadeProxy

`CascadeProxy` is an example that shows an aggregator server that forwards
the requests to another proxy server (end proxy).

Diagram:
```
client --> middle proxy --> end proxy --> internet
```

This example starts both proxy servers using goproxy, the middle one
listens on port `8081`, and the end one on port `8082`.

The middle proxy must be an HTTP server, since we use goproxy that
expose only it.
The end proxy can be any type of proxy supported by Go, including SOCKS5,
there is a comment in the part where you can put its address.
