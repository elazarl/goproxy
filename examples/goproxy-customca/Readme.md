# Transparent proxy with custom CA

This transparent example in goproxy is meant to show how to transparenty proxy and hijack all http and https connections while doing a man-in-the-middle to the TLS session.
You need to configure routing rules for your system and add injected into main.go certificate to your system as trusted.
