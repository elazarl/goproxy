# CascadeSocksProxy

`CascadeSocksProxy` is an example that shows an aggregator server that forwards
the requests to another socks proxy server. This example is written base on `cascadeproxy` example.

Diagram:
```
client --> goproxy --> socks5 proxy --> internet
```

This example starts a HTTP/HTTPS proxy using goproxy that listens on port `8080`, and forward the requests to the socks5 proxy on `socks5://localhost:1080`.
It uses MITM (Man in the Middle) proxy mode to retriece and parse the request, and then forwards it to the destination using  the socks5 proxy client implemented in the standard Go `net/http` library. 

### Example usage:

Aggregator server that have HTTP proxy server run on port `8080` and forward the requests to socks proxy listens on `socks5://localhost:1080` with no auth
```shell
./socks -v -addr ":8080" -socks "localhost:1080"
``` 

With auth:
```shell
./socks -v -addr ":8080" -socks "localhost:1080" -user "bob" -pass "123"
 ```

You can run the socks proxy server locally for testing with the following command - this will start a socks5 proxy server on port `1080` with no auth:
```shell
./socks5proxyserver/socks5proxyserver
```