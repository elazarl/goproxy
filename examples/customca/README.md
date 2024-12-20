# CustomCA

This example shows you how to use a custom CA to sign the HTTPS MITM
requests (you can use your own certificate).
This certificate must be trusted by your system, or the client will fail, if it's
not recognized.
The custom certificate is used to read the request data of an HTTPS
connection.
If the client has some kind of SSL pinning to check the certificates, the
request will most likely fail, so make sure to remove it.