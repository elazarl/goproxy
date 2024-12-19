# CascadeSocksProxy

`CascadeSocksProxy` is an example that shows an aggregator server that forwards
the requests to another socks proxy server. This example is written base on `cascadeproxy` example.

Diagram:
```
client --> goproxy --> socks5 proxy --> internet
```

This example starts a HTTP/HTTPS proxy using goproxy that listens on port `8080`, and forward the requests to the socks5 proxy on `socks5://localhost:1080`.


### Example usage:
Aggregator server that have HTTP proxy server run on port `8080` and forward the requests to socks proxy with no auth
```shell
./socks -v -addr ":8080" -socks "10.1.1.1:1080"
``` 

With auth:
```shell
./socks -v -addr ":8080" -socks "localhost:1080" -user "bob" -pass "123"
 ```