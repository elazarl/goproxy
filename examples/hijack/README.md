# Hijack
In this example we intercept the data of an HTTP request and decide to
modify them before sending to the client.
In this mode, we take over on the raw connection and we could send any
data that we want.

Curl example:

```
$ curl -x localhost:8080 http://google.it -v -k -p

* Host localhost:8080 was resolved.
* IPv6: ::1
* IPv4: 127.0.0.1
*   Trying [::1]:8080...
* Connected to localhost (::1) port 8080
* CONNECT tunnel: HTTP/1.1 negotiated
* allocate connect buffer
* Establish HTTP proxy tunnel to google.it:80
> CONNECT google.it:80 HTTP/1.1
> Host: google.it:80
> User-Agent: curl/8.9.1
> Proxy-Connection: Keep-Alive
>
< HTTP/1.1 200 Ok
<
* CONNECT phase completed
* CONNECT tunnel established, response 200
> GET / HTTP/1.1
> Host: google.it
> User-Agent: curl/8.9.1
> Accept: */*
>
< HTTP/1.1 200 OK
< test: 1234
< Content-Length: 0
```
