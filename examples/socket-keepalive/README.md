# Socket KeepAlive

`socket-keepalive` example adds a custom net.Dialer that can be configured
by the user, enabling TCP keep alives in this example.
By default, Go already uses 15 seconds TCP keep alives for the connections,
so this example is not strictly required, as it is provided.
TCP keep alives are useful for a connection that can be idle for a while,
to avoid the TCP connection close.
The TCP connection is closed when the request context expires.