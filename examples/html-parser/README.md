# HTML Parser

`html-parser` starts an HTTP proxy on :8080.
It checks HTML responses, looks for scripts referencing jQuery library
and log warnings if different versions of the library are being used
for a given host.
This is an example of how a proxy can parse the received responses and
manipulate them to do useful actions.

Start the server:

```sh
go build
html-parser
```

Make a test request in another shell:

```sh
http_proxy=http://127.0.0.1:8080 wget -O - \
	http://ripper234.com/p/introducing-goproxy-light-http-proxy/
```

Goproxy example homepage contains jQuery and a mix of JQuery plugins.
First the proxy reports the first use of jQuery it detects for the domain.
Then, because the regular expression matching the jQuery sources is imprecise,
it reports a mismatch with a plugin reference:

```sh
2015/04/11 11:23:02 [001] WARN: ripper234.com uses //ajax.googleapis.com/ajax/libs/jquery/1.11.1/jquery.min.js
2015/04/11 11:23:02 [001] WARN: In http://ripper234.com/p/introducing-goproxy-light-http-proxy/, \
  Contradicting jqueries //ajax.googleapis.com/ajax/libs/jquery/1.11.1/jquery.min.js \
  http://ripper234.wpengine.netdna-cdn.com/wp-content/plugins/wp-ajax-edit-comments/js/jquery.colorbox.min.js?ver=5.0.36
```
