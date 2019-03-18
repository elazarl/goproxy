package details

import "net"

type RoundTripDetails struct {
	Host    string
	TCPAddr *net.TCPAddr
	IsProxy bool
	Error   error
}
