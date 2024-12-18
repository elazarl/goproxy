# Request Filtering

`request-filtering` starts an HTTP proxy on :8080. It denies requests
to "www.reddit.com" made between 8am to 5pm inclusive, local server
time.

Start the server:

```sh
$ request-filtering
```

Make a test request in another shell:

```sh
$ http_proxy=http://127.0.0.1:8080 wget -O - http://www.reddit.com
--2015-04-11 16:59:01--  http://www.reddit.com/
Connecting to 127.0.0.1:8080... connected.
Proxy request sent, awaiting response... 403 Forbidden
2015-04-11 16:59:01 ERROR 403: Forbidden.
```
