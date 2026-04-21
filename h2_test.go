package goproxy

import (
	"bytes"
	"io"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestProxyFrameRewritesHeaders(t *testing.T) {
	var input bytes.Buffer
	block := encodeHeaderBlock(t,
		H2HeaderField{Name: ":method", Value: "GET"},
		H2HeaderField{Name: "authorization", Value: "Basic old", Sensitive: true},
	)
	inputFramer := http2.NewFramer(&input, nil)
	if err := inputFramer.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      1,
		BlockFragment: block,
		EndHeaders:    true,
		EndStream:     true,
	}); err != nil {
		t.Fatalf("write input headers: %v", err)
	}

	var output bytes.Buffer
	fr := http2.NewFramer(&output, bytes.NewReader(input.Bytes()))
	rewriter := newH2HeaderBlockRewriter(func(fields []H2HeaderField) []H2HeaderField {
		out := append([]H2HeaderField(nil), fields...)
		for i := range out {
			if out[i].Name == "authorization" {
				out[i].Value = "Basic new"
			}
		}
		return out
	})
	if err := proxyFrame(fr, rewriter); err != nil && err != io.EOF {
		t.Fatalf("proxy frame: %v", err)
	}

	fields := readHeaderBlock(t, output.Bytes())
	if got := headerValue(fields, "authorization"); got != "Basic new" {
		t.Fatalf("authorization mismatch: got %q", got)
	}
}

func encodeHeaderBlock(t *testing.T, fields ...H2HeaderField) []byte {
	t.Helper()
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for _, field := range fields {
		if err := enc.WriteField(hpack.HeaderField{Name: field.Name, Value: field.Value, Sensitive: field.Sensitive}); err != nil {
			t.Fatalf("encode header %q: %v", field.Name, err)
		}
	}
	return block.Bytes()
}

func readHeaderBlock(t *testing.T, data []byte) []H2HeaderField {
	t.Helper()
	fr := http2.NewFramer(io.Discard, bytes.NewReader(data))
	var block bytes.Buffer
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			t.Fatalf("read output frame: %v", err)
		}
		switch f := frame.(type) {
		case *http2.HeadersFrame:
			block.Write(f.HeaderBlockFragment())
			if f.HeadersEnded() {
				return decodeHeaderBlock(t, block.Bytes())
			}
		case *http2.ContinuationFrame:
			block.Write(f.HeaderBlockFragment())
			if f.HeadersEnded() {
				return decodeHeaderBlock(t, block.Bytes())
			}
		default:
			t.Fatalf("unexpected frame type %T", f)
		}
	}
}

func decodeHeaderBlock(t *testing.T, block []byte) []H2HeaderField {
	t.Helper()
	var fields []H2HeaderField
	decoder := hpack.NewDecoder(4096, func(field hpack.HeaderField) {
		fields = append(fields, H2HeaderField{Name: field.Name, Value: field.Value, Sensitive: field.Sensitive})
	})
	if _, err := decoder.Write(block); err != nil {
		t.Fatalf("decode block write: %v", err)
	}
	if err := decoder.Close(); err != nil {
		t.Fatalf("decode block close: %v", err)
	}
	return fields
}

func headerValue(fields []H2HeaderField, name string) string {
	for _, field := range fields {
		if field.Name == name {
			return field.Value
		}
	}
	return ""
}
