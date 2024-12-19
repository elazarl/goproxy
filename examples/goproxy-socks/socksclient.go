// This code was wrote base on
// https://cs.opensource.google/go/x/net/+/refs/tags/v0.32.0:internal/socks/socks.go

package main

import (
	"context"
	"errors"
	"github.com/elazarl/goproxy"
	"io"
	"net"
	"net/http"
	"strconv"
)

// A Command represents a SOCKS command.
type Command int

func (cmd Command) String() string {
	switch cmd {
	case CmdConnect:
		return "socks connect"
	case cmdBind:
		return "socks bind"
	default:
		return "socks " + strconv.Itoa(int(cmd))
	}
}

// An AuthMethod represents a SOCKS authentication method.
type AuthMethod int

// A Reply represents a SOCKS command reply code.
type Reply int

func (code Reply) String() string {
	switch code {
	case StatusSucceeded:
		return "succeeded"
	case 0x01:
		return "general SOCKS server failure"
	case 0x02:
		return "connection not allowed by ruleset"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return "unknown code: " + strconv.Itoa(int(code))
	}
}

// Wire protocol constants.
const (
	Version5 = 0x05

	AddrTypeIPv4 = 0x01
	AddrTypeFQDN = 0x03
	AddrTypeIPv6 = 0x04

	CmdConnect Command = 0x01 // establishes an active-open forward proxy connection
	cmdBind    Command = 0x02 // establishes a passive-open forward proxy connection

	AuthMethodNotRequired         AuthMethod = 0x00 // no authentication required
	AuthMethodUsernamePassword    AuthMethod = 0x02 // use username/password
	AuthMethodNoAcceptableMethods AuthMethod = 0xff // no acceptable authentication methods

	StatusSucceeded Reply = 0x00
)

type SocksAuth struct {
	Username, Password string
}

type Addr struct {
	Name string
	IP   net.IP
	Port int
}

func NewSocks5ConnectDialedToProxy(proxy *goproxy.ProxyHttpServer, https_proxy string, auth *SocksAuth, connectReqHandler func(req *http.Request)) (func(network string, addr string) (net.Conn, error), error) {
	c, err := net.Dial("tcp", https_proxy)
	if err != nil {
		return nil, err
	}
	return func(network, addr string) (net.Conn, error) {
		host, port, err := splitHostPort(addr)
		if err != nil {
			return nil, err
		}
		b := make([]byte, 0, 6+len(host)) // the size here is just an estimate
		b = append(b, Version5)
		var Authenticate func(ctx context.Context, rw io.ReadWriter, auth AuthMethod) error
		if auth == nil || len(auth.Username) == 0 {
			b = append(b, 1, byte(AuthMethodNotRequired))
		} else {
			Authenticate = auth.Authenticate
			b = append(b, byte(AuthMethodUsernamePassword))
		}
		if _, err = c.Write(b); err != nil {
			return nil, err
		}

		if _, err = io.ReadFull(c, b[:2]); err != nil {
			return nil, err
		}
		if b[0] != Version5 {
			return nil, errors.New("unexpected protocol version " + strconv.Itoa(int(b[0])))
		}
		am := AuthMethod(b[1])
		if am == AuthMethodNoAcceptableMethods {
			return nil, errors.New("no acceptable authentication methods")
		}
		if Authenticate != nil {
			if err = Authenticate(context.Background(), c, am); err != nil {
				return nil, err
			}
		}

		b = b[:0]
		b = append(b, Version5, byte(CmdConnect), 0)
		if ip := net.ParseIP(host); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				b = append(b, AddrTypeIPv4)
				b = append(b, ip4...)
			} else if ip6 := ip.To16(); ip6 != nil {
				b = append(b, AddrTypeIPv6)
				b = append(b, ip6...)
			} else {
				return nil, errors.New("unknown address type")
			}
		} else {
			if len(host) > 255 {
				return nil, errors.New("FQDN too long")
			}
			b = append(b, AddrTypeFQDN)
			b = append(b, byte(len(host)))
			b = append(b, host...)
		}
		b = append(b, byte(port>>8), byte(port))
		if _, err = c.Write(b); err != nil {
			return nil, err
		}

		if _, err = io.ReadFull(c, b[:4]); err != nil {
			return nil, err
		}
		if b[0] != Version5 {
			return nil, errors.New("unexpected protocol version " + strconv.Itoa(int(b[0])))
		}
		if cmdErr := Reply(b[1]); cmdErr != StatusSucceeded {
			return nil, errors.New("unknown error " + cmdErr.String())
		}
		if b[2] != 0 {
			return nil, errors.New("non-zero reserved field")
		}
		l := 2
		var a Addr
		switch b[3] {
		case AddrTypeIPv4:
			l += net.IPv4len
			a.IP = make(net.IP, net.IPv4len)
		case AddrTypeIPv6:
			l += net.IPv6len
			a.IP = make(net.IP, net.IPv6len)
		case AddrTypeFQDN:
			if _, err := io.ReadFull(c, b[:1]); err != nil {
				return nil, err
			}
			l += int(b[0])
		default:
			return nil, errors.New("unknown address type " + strconv.Itoa(int(b[3])))
		}
		if cap(b) < l {
			b = make([]byte, l)
		} else {
			b = b[:l]
		}
		if _, err = io.ReadFull(c, b); err != nil {
			return nil, err
		}
		if a.IP != nil {
			copy(a.IP, b)
		} else {
			a.Name = string(b[:len(b)-2])
		}
		a.Port = int(b[len(b)-2])<<8 | int(b[len(b)-1])
		return c, nil
	}, nil
}

const (
	authUsernamePasswordVersion = 0x01
	authStatusSucceeded         = 0x00
)

func (up *SocksAuth) Authenticate(ctx context.Context, rw io.ReadWriter, auth AuthMethod) error {
	switch auth {
	case AuthMethodNotRequired:
		return nil
	case AuthMethodUsernamePassword:
		if len(up.Username) == 0 || len(up.Username) > 255 || len(up.Password) > 255 {
			return errors.New("invalid username/password")
		}
		b := []byte{authUsernamePasswordVersion}
		b = append(b, byte(len(up.Username)))
		b = append(b, up.Username...)
		b = append(b, byte(len(up.Password)))
		b = append(b, up.Password...)

		if _, err := rw.Write(b); err != nil {
			return err
		}
		if _, err := io.ReadFull(rw, b[:2]); err != nil {
			return err
		}
		if b[0] != authUsernamePasswordVersion {
			return errors.New("invalid username/password version")
		}
		if b[1] != authStatusSucceeded {
			return errors.New("username/password authentication failed")
		}
		return nil
	}
	return errors.New("unsupported authentication method " + strconv.Itoa(int(auth)))
}

func splitHostPort(address string) (string, int, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	portnum, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, err
	}
	if 1 > portnum || portnum > 0xffff {
		return "", 0, errors.New("port number out of range " + port)
	}
	ip, err := net.ResolveIPAddr("ip", host)
	if err != nil {
		return "", 0, err
	}
	return ip.String(), portnum, nil
}
