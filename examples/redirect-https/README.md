# Redirect HTTPS

`redirect-https` example redirects all the HTTPS request to HTTP endpoint,
by returning a `303 See Other` HTTP response to the client.
The client will then make another request using the HTTP scheme.
