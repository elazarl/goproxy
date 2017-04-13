package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

var errNoHeader = errors.New("Header doesn't exist")

func timeHeader(key string, h http.Header) (time.Time, error) {
	if header := h.Get(key); header != "" {
		return http.ParseTime(header)
	} else {
		return time.Time{}, errNoHeader
	}
}

func intHeader(key string, h http.Header) (int, error) {
	if header := h.Get(key); header != "" {
		return strconv.Atoi(header)
	} else {
		return 0, errNoHeader
	}
}
