package goproxy

import "strings"

// inheritPort returns a newHost that falls back to the port on the
// `previousHost`, and if not present falls back to ":80"
func inheritPort(newHost, previousHost string) string {
	newIx := strings.IndexRune(newHost, ':')
	if newIx == -1 {
		previousIx := strings.IndexRune(previousHost, ':')
		if previousIx == -1 {
			return newHost + ":80"
		}
		return newHost + previousHost[previousIx:]
	}
	return newHost
}
