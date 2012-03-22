
/*
Package goproxy provides a customizable HTTP proxy,
supporting hijacking HTTPS connection.

The intent of the proxy, is to be usable with reasonable amount of traffic
yet, customizable and programable.

The proxy itself is simply an `net/http` handler.

Example use cases:

https://github.com/elazarl/goproxy/examples/avgSize

To measure the average size of an Html served in your site. One can ask
all the QA team to access the website by a proxy, and the proxy will
measure the average size of all text/html responses from your host.

[not yet implemented]

All requests to your web servers should be directed through the proxy,
when the proxy will detect html pieces sent as a response to AJAX
request, it'll send a warning email.

[not yet implemented]

Generate a real traffic to your website by real users using through
proxy. Record the traffic, and try it again for more real load testing.

*/
package goproxy
