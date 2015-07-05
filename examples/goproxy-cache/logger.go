package main

import "log"

const (
	ansiRed   = "\x1b[31;1m"
	ansiReset = "\x1b[0m"
)

var DebugLogging = false

func debugf(format string, args ...interface{}) {
	if DebugLogging {
		log.Printf(format, args...)
	}
}

func errorf(format string, args ...interface{}) {
	log.Printf(ansiRed+"✗ "+format+ansiReset, args)
}
