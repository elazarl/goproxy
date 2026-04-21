package goproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

var ErrInvalidH2Frame = errors.New("invalid H2 frame")

// H2Transport is an implementation of RoundTripper that abstracts an entire
// HTTP/2 session, sending all client frames to the server and responses back
// to the client.
type H2Transport struct {
	ClientReader   io.Reader
	ClientWriter   io.Writer
	TLSConfig      *tls.Config
	Host           string
	RewriteHeaders func([]H2HeaderField) []H2HeaderField
}

// H2HeaderField is a generic, mutable representation of a single HTTP/2
// header field.
type H2HeaderField struct {
	Name      string
	Value     string
	Sensitive bool
}

// H2HeaderRewriter can be optionally implemented by a custom RoundTripper
// when the downstream MITM path upgrades to HTTP/2 and callers need to
// mutate decoded request headers before they are forwarded upstream.
type H2HeaderRewriter interface {
	RewriteH2HeaderFields([]H2HeaderField) []H2HeaderField
}

// RoundTrip executes an HTTP/2 session (including all contained streams).
// The request and response are ignored but any error encountered during the
// proxying from the session is returned as a result of the invocation.
func (r *H2Transport) RoundTrip(_ *http.Request) (*http.Response, error) {
	raddr := r.Host
	if !strings.Contains(raddr, ":") {
		raddr += ":443"
	}
	rawServerTLS, err := dial("tcp", raddr)
	if err != nil {
		return nil, err
	}
	defer rawServerTLS.Close()
	// Ensure that we only advertise HTTP/2 as the accepted protocol.
	r.TLSConfig.NextProtos = []string{http2.NextProtoTLS}
	// Initiate TLS and check remote host name against certificate.
	rawServerTLS = tls.Client(rawServerTLS, r.TLSConfig)
	rawTLSConn, ok := rawServerTLS.(*tls.Conn)
	if !ok {
		return nil, errors.New("invalid TLS connection")
	}
	if err = rawTLSConn.HandshakeContext(context.Background()); err != nil {
		return nil, err
	}
	if r.TLSConfig == nil || !r.TLSConfig.InsecureSkipVerify {
		if err = rawTLSConn.VerifyHostname(raddr[:strings.LastIndex(raddr, ":")]); err != nil {
			return nil, err
		}
	}
	// Send new client preface to match the one parsed in req.
	if _, err := io.WriteString(rawServerTLS, http2.ClientPreface); err != nil {
		return nil, err
	}
	serverTLSReader := bufio.NewReader(rawServerTLS)
	cToS := http2.NewFramer(rawServerTLS, r.ClientReader)
	sToC := http2.NewFramer(r.ClientWriter, serverTLSReader)
	cToSRewriter := newH2HeaderBlockRewriter(r.RewriteHeaders)
	errSToC := make(chan error)
	errCToS := make(chan error)
	go func() {
		for {
			if err := proxyFrame(sToC, nil); err != nil {
				errSToC <- err
				break
			}
		}
	}()
	go func() {
		for {
			if err := proxyFrame(cToS, cToSRewriter); err != nil {
				errCToS <- err
				break
			}
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errSToC:
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
		case err := <-errCToS:
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
		}
	}
	return nil, nil
}

func dial(network, addr string) (c net.Conn, err error) {
	addri, err := net.ResolveTCPAddr(network, addr)
	if err != nil {
		return
	}
	c, err = net.DialTCP(network, nil, addri)
	return
}

const maxH2FrameSize = 16384

type h2HeaderBlockRewriter struct {
	rewrite func([]H2HeaderField) []H2HeaderField
	decoder *hpack.Decoder
	fields  []hpack.HeaderField
	pending *pendingHeaderBlock
}

type pendingHeaderBlock struct {
	streamID  uint32
	endStream bool
	priority  http2.PriorityParam
	fragments [][]byte
}

func newH2HeaderBlockRewriter(rewrite func([]H2HeaderField) []H2HeaderField) *h2HeaderBlockRewriter {
	if rewrite == nil {
		return nil
	}
	rewriter := &h2HeaderBlockRewriter{rewrite: rewrite}
	rewriter.decoder = hpack.NewDecoder(4096, func(f hpack.HeaderField) {
		rewriter.fields = append(rewriter.fields, f)
	})
	return rewriter
}

// proxyFrame reads a single frame from the Framer and, when successful, writes
// a ~identical one back to the Framer.
func proxyFrame(fr *http2.Framer, rewriter *h2HeaderBlockRewriter) error {
	f, err := fr.ReadFrame()
	if err != nil {
		return err
	}
	switch f.Header().Type {
	case http2.FrameData:
		tf, ok := f.(*http2.DataFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		terr := fr.WriteData(tf.StreamID, tf.StreamEnded(), tf.Data())
		if terr == nil && tf.StreamEnded() {
			terr = io.EOF
		}
		return terr
	case http2.FrameHeaders:
		tf, ok := f.(*http2.HeadersFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		if rewriter != nil {
			return rewriter.handleHeaders(fr, tf)
		}
		terr := fr.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      tf.StreamID,
			BlockFragment: tf.HeaderBlockFragment(),
			EndStream:     tf.StreamEnded(),
			EndHeaders:    tf.HeadersEnded(),
			PadLength:     0,
			Priority:      tf.Priority,
		})
		if terr == nil && tf.StreamEnded() {
			terr = io.EOF
		}
		return terr
	case http2.FrameContinuation:
		tf, ok := f.(*http2.ContinuationFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		if rewriter != nil {
			return rewriter.handleContinuation(fr, tf)
		}
		return fr.WriteContinuation(tf.StreamID, tf.HeadersEnded(), tf.HeaderBlockFragment())
	case http2.FrameGoAway:
		tf, ok := f.(*http2.GoAwayFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		return fr.WriteGoAway(tf.StreamID, tf.ErrCode, tf.DebugData())
	case http2.FramePing:
		tf, ok := f.(*http2.PingFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		return fr.WritePing(tf.IsAck(), tf.Data)
	case http2.FrameRSTStream:
		tf, ok := f.(*http2.RSTStreamFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		return fr.WriteRSTStream(tf.StreamID, tf.ErrCode)
	case http2.FrameSettings:
		tf, ok := f.(*http2.SettingsFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		if tf.IsAck() {
			return fr.WriteSettingsAck()
		}
		var settings []http2.Setting
		// NOTE: If we want to parse headers, need to handle
		// settings where s.ID == http2.SettingHeaderTableSize and
		// accordingly update the Framer options.
		for i := 0; i < tf.NumSettings(); i++ {
			settings = append(settings, tf.Setting(i))
		}
		return fr.WriteSettings(settings...)
	case http2.FrameWindowUpdate:
		tf, ok := f.(*http2.WindowUpdateFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		return fr.WriteWindowUpdate(tf.StreamID, tf.Increment)
	case http2.FramePriority:
		tf, ok := f.(*http2.PriorityFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		return fr.WritePriority(tf.StreamID, tf.PriorityParam)
	case http2.FramePushPromise:
		tf, ok := f.(*http2.PushPromiseFrame)
		if !ok {
			return ErrInvalidH2Frame
		}
		return fr.WritePushPromise(http2.PushPromiseParam{
			StreamID:      tf.StreamID,
			PromiseID:     tf.PromiseID,
			BlockFragment: tf.HeaderBlockFragment(),
			EndHeaders:    tf.HeadersEnded(),
			PadLength:     0,
		})
	default:
		return errors.New("Unsupported frame: " + string(f.Header().Type))
	}
}

func (rw *h2HeaderBlockRewriter) handleHeaders(fr *http2.Framer, hf *http2.HeadersFrame) error {
	if hf.HeadersEnded() {
		return rw.flush(fr, hf.StreamID, hf.StreamEnded(), hf.Priority, hf.HeaderBlockFragment())
	}
	rw.pending = &pendingHeaderBlock{
		streamID:  hf.StreamID,
		endStream: hf.StreamEnded(),
		priority:  hf.Priority,
		fragments: [][]byte{append([]byte(nil), hf.HeaderBlockFragment()...)},
	}
	return nil
}

func (rw *h2HeaderBlockRewriter) handleContinuation(fr *http2.Framer, cf *http2.ContinuationFrame) error {
	if rw.pending == nil || rw.pending.streamID != cf.StreamID {
		return ErrInvalidH2Frame
	}
	rw.pending.fragments = append(rw.pending.fragments, append([]byte(nil), cf.HeaderBlockFragment()...))
	if !cf.HeadersEnded() {
		return nil
	}
	var block []byte
	for _, fragment := range rw.pending.fragments {
		block = append(block, fragment...)
	}
	pending := rw.pending
	rw.pending = nil
	return rw.flush(fr, pending.streamID, pending.endStream, pending.priority, block)
}

func (rw *h2HeaderBlockRewriter) flush(fr *http2.Framer, streamID uint32, endStream bool, priority http2.PriorityParam, block []byte) error {
	rw.fields = nil
	if _, err := rw.decoder.Write(block); err != nil {
		return err
	}
	if err := rw.decoder.Close(); err != nil {
		return err
	}
	fields := make([]H2HeaderField, 0, len(rw.fields))
	for _, field := range rw.fields {
		fields = append(fields, H2HeaderField{Name: field.Name, Value: field.Value, Sensitive: field.Sensitive})
	}
	fields = rw.rewrite(fields)
	var encoded bytes.Buffer
	encoder := hpack.NewEncoder(&encoded)
	for _, field := range fields {
		if err := encoder.WriteField(hpack.HeaderField{Name: field.Name, Value: field.Value, Sensitive: field.Sensitive}); err != nil {
			return err
		}
	}
	return writeHeaderBlock(fr, streamID, endStream, priority, encoded.Bytes())
}

func writeHeaderBlock(fr *http2.Framer, streamID uint32, endStream bool, priority http2.PriorityParam, block []byte) error {
	if len(block) <= maxH2FrameSize {
		return fr.WriteHeaders(http2.HeadersFrameParam{
			StreamID:      streamID,
			BlockFragment: block,
			EndStream:     endStream,
			EndHeaders:    true,
			Priority:      priority,
		})
	}
	if err := fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      streamID,
		BlockFragment: block[:maxH2FrameSize],
		EndStream:     endStream,
		EndHeaders:    false,
		Priority:      priority,
	}); err != nil {
		return err
	}
	block = block[maxH2FrameSize:]
	for len(block) > maxH2FrameSize {
		if err := fr.WriteContinuation(streamID, false, block[:maxH2FrameSize]); err != nil {
			return err
		}
		block = block[maxH2FrameSize:]
	}
	return fr.WriteContinuation(streamID, true, block)
}
