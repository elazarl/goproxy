package goproxy

import (
	"net"
)

type Resolver interface {
	Resolve(string) (*net.TCPAddr, error)
}
