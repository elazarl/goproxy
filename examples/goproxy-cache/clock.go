package main

import (
	"time"
)

var Clock = func() time.Time {
	return time.Now().UTC()
}