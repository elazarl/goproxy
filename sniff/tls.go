package sniff

import (
	"crypto/tls"
	_ "unsafe"
)

type transcriptHash interface {
	Write([]byte) (int, error)
}

//go:linkname readHandshake crypto/tls.(*Conn).readHandshake
func readHandshake(conn *tls.Conn, transcript transcriptHash) (any, error)
