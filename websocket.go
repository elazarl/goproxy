package goproxy

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func headerContains(header http.Header, name string, value string) bool {
	for _, v := range header[name] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(value, strings.TrimSpace(s)) {
				return true
			}
		}
	}
	return false
}

func isWebSocketRequest(r *http.Request) bool {
	return headerContains(r.Header, "Connection", "upgrade") &&
		headerContains(r.Header, "Upgrade", "websocket")
}

func (proxy *ProxyHttpServer) serveWebsocketTLS(ctx *ProxyCtx, w http.ResponseWriter, req *http.Request, tlsConfig *tls.Config, clientConn *tls.Conn) {
	targetURL := url.URL{Scheme: "wss", Host: req.URL.Host, Path: req.URL.Path}

	// Connect to upstream
	targetConn, err := tls.Dial("tcp", targetURL.Host, tlsConfig)
	if err != nil {
		ctx.Warnf("Error dialing target site: %v", err)
		return
	}
	defer targetConn.Close()

	// Perform handshake
	if err := proxy.websocketHandshake(ctx, req, targetConn, clientConn); err != nil {
		ctx.Warnf("Websocket handshake error: %v", err)
		return
	}

	// Proxy wss connection
	proxy.proxyWebsocket(ctx, targetConn, clientConn)
}

func (proxy *ProxyHttpServer) serveWebsocket(ctx *ProxyCtx, w http.ResponseWriter, req *http.Request) {
	targetURL := url.URL{Scheme: "ws", Host: req.URL.Host, Path: req.URL.Path}

	targetConn, err := proxy.connectDial("tcp", targetURL.Host)
	if err != nil {
		ctx.Warnf("Error dialing target site: %v", err)
		return
	}
	defer targetConn.Close()

	// Connect to Client
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		ctx.Warnf("Hijack error: %v", err)
		return
	}

	// Perform handshake
	if err := proxy.websocketHandshake(ctx, req, targetConn, clientConn); err != nil {
		ctx.Warnf("Websocket handshake error: %v", err)
		return
	}

	// Proxy ws connection
	proxy.proxyWebsocket(ctx, targetConn, clientConn)
}

func (proxy *ProxyHttpServer) websocketHandshake(ctx *ProxyCtx, req *http.Request, targetSiteConn io.ReadWriter, clientConn io.ReadWriter) error {
	// write handshake request to target
	err := req.Write(targetSiteConn)
	if err != nil {
		ctx.Warnf("Error writing upgrade request: %v", err)
		return err
	}

	targetTLSReader := bufio.NewReader(targetSiteConn)

	// Read handshake response from target
	resp, err := http.ReadResponse(targetTLSReader, req)
	if err != nil {
		ctx.Warnf("Error reading handhsake response  %v", err)
		return err
	}

	// Run response through handlers
	resp = proxy.filterResponse(resp, ctx)

	// Proxy handshake back to client
	err = resp.Write(clientConn)
	if err != nil {
		ctx.Warnf("Error writing handshake response: %v", err)
		return err
	}
	return nil
}

type websocketPacket struct {
	valid         bool
	flags         byte
	opcode        int
	mask          bool
	payloadLength int
	maskingKey    []byte
	payload       []byte
	packetSize    int
}

func newWebsocketPacket(packet []byte) *websocketPacket {
	p := &websocketPacket{}
	p.valid = true
	p.flags = packet[0] & 0xF0
	p.opcode = int(packet[0] & 0x0F)
	p.mask = (packet[1] & 0x80) == 0x80
	p.payloadLength = int(packet[1] & 0x7f)
	packetStart := 2
	if p.payloadLength == 126 {
		p.payloadLength = int(packet[2])<<8 | int(packet[3])
		packetStart += 2
		p.maskingKey = packet[4:8]
	} else if p.payloadLength == 127 {
		p.payloadLength = int(packet[2])<<56 | int(packet[3])<<48 | int(packet[4])<<40 | int(packet[5])<<32 | int(packet[6])<<24 | int(packet[7])<<16 | int(packet[8])<<8 | int(packet[9])
		packetStart += 8
		p.maskingKey = packet[10:14]
	} else {
		p.maskingKey = packet[2:6]
	}

	if !p.mask {
		p.maskingKey = nil
	} else {
		packetStart += 4
	}

	p.packetSize = packetStart + p.payloadLength
	p.payload = packet[packetStart:p.packetSize]
	p.valid = (len(p.payload) == p.payloadLength)

	if p.valid && p.maskingKey != nil {
		for i := 0; i < p.payloadLength; i++ {
			p.payload[i] ^= p.maskingKey[i%4]
		}
	}

	return p
}

func (packet *websocketPacket) encode() []byte {
	packetLength := 2 + len(packet.maskingKey) + len(packet.payload)
	packetStart := 2 + len(packet.maskingKey)
	maskingKeyStart := 2

	packet.payloadLength = len(packet.payload)

	if packet.payloadLength > 125 {
		packetLength += 2
		packetStart += 2
		maskingKeyStart += 2
	}

	if packet.payloadLength > 65535 {
		packetLength += 6
		packetStart += 6
		maskingKeyStart += 6
	}

	buf := make([]byte, packetLength)
	buf[0] = byte(packet.flags) | byte(packet.opcode)

	// encode the length
	if packet.payloadLength < 126 {
		buf[1] = byte(packet.payloadLength)
	} else if packet.payloadLength < 65536 {
		buf[1] = 126
		binary.BigEndian.PutUint16(buf[2:], uint16(packet.payloadLength))
	} else {
		buf[1] = 127
		binary.BigEndian.PutUint64(buf[2:], uint64(packet.payloadLength))
	}

	if packet.maskingKey != nil {
		buf[1] |= 0x80
	}

	// reencode using the masking key
	if packet.maskingKey != nil {
		copy(buf[maskingKeyStart:], packet.maskingKey)
		copy(buf[packetStart:], packet.payload)
		for i := 0; i < len(packet.payload); i++ {
			buf[i+packetStart] ^= packet.maskingKey[i%4]
		}
	} else {
		copy(buf[packetStart:], packet.payload)
	}

	return buf
}

func (proxy *ProxyHttpServer) copyWebsocketData(dst io.Writer, src io.Reader, direction WebsocketDirection, errChan chan error, ctx *ProxyCtx) {
	fullPacket := make([]byte, 0)
	buf := make([]byte, 32*1024)
	var err error = nil

	for {
		nr, er := src.Read(buf)

		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}

		if nr > 0 {
			fullPacket = append(fullPacket, buf[:nr]...)
			websocketPacket := newWebsocketPacket(fullPacket)

			if !websocketPacket.valid {
				continue
			}

			websocketPacket.payload = proxy.filterWebsocketPacket(websocketPacket.payload, direction, ctx)
			encodedPacket := websocketPacket.encode()
			nw, ew := dst.Write(encodedPacket)
			fullPacket = fullPacket[websocketPacket.packetSize:]

			if nw < 0 || len(encodedPacket) < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			if ew != nil {
				err = ew
				break
			}
			if len(encodedPacket) != nw {
				err = io.ErrShortWrite
				break
			}
		}

	}

	ctx.Warnf("Websocket error: %v", err)
	errChan <- err
}

func (proxy *ProxyHttpServer) proxyWebsocket(ctx *ProxyCtx, dest io.ReadWriter, source io.ReadWriter) {
	errChan := make(chan error, 2)

	// Start proxying websocket data
	go proxy.copyWebsocketData(dest, source, ClientToServer, errChan, ctx)
	go proxy.copyWebsocketData(source, dest, ServerToClient, errChan, ctx)
	<-errChan
}
