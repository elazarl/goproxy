package regretable

import (
	"io"
)

// A RegretOnceBuffer will allow you to read from a reader, and then
// to "regret" reading it, and push back everything you've read.
// For example,
//	rb := NewRegretOnceBuffer(bytes.NewBuffer([]byte{1,2,3}))
//	var b = make([]byte,1)
//	rb.Read(b) // b[0] = 1
//	rb.Regret()
//	ioutil.ReadAll(rb.Read) // returns []byte{1,2,3},nil
type RegretOnceBuffer struct {
	reader   io.Reader
	overflow bool
	r, w     int
	buf      []byte
}

var defaultBufferSize = 500

// Same as RegretOnceBuffer, but allows closing the underlying reader
type RegretOnceBufferCloser struct {
	RegretOnceBuffer
	c      io.Closer
}

// Closes the underlying readCloser, you cannot regret after closing the stream
func (rbc *RegretOnceBufferCloser) Close() error {
	return rbc.c.Close()
}

// initialize a RegretOnceBufferCloser with underlying readCloser rc
func NewRegretOnceBufferCloser(rc io.ReadCloser) *RegretOnceBufferCloser {
	return &RegretOnceBufferCloser{*NewRegretOnceBuffer(rc), rc}
}

// The next read from the RegretOnceBuffer will be as if the underlying reader
// was never read (or from the last point forget is called).
func (rb *RegretOnceBuffer) Regret() {
	if rb.overflow {
		panic("regretting after overflow makes no sense")
	}
	rb.r = 0
}

// Will "forget" everything read so far.
//	rb := NewRegretOnceBuffer(bytes.NewBuffer([]byte{1,2,3}))
//	var b = make([]byte,1)
//	rb.Read(b) // b[0] = 1
//	rb.Forget()
//	rb.Read(b) // b[0] = 2
//	rb.Regret()
//	ioutil.ReadAll(rb.Read) // returns []byte{2,3},nil
func (rb *RegretOnceBuffer) Forget() {
	if rb.overflow {
		panic("forgetting after overflow makes no sense")
	}
	rb.r = 0
	rb.w = 0
}

// initialize a RegretOnceBuffer with underlying reader r, whose buffer is size bytes long
func NewRegretOnceBufferSize(r io.Reader, size int) *RegretOnceBuffer {
	return &RegretOnceBuffer{reader: r, buf: make([]byte, size) }
}

// initialize a RegretOnceBuffer with underlying reader r
func NewRegretOnceBuffer(r io.Reader) *RegretOnceBuffer {
	return NewRegretOnceBufferSize(r, defaultBufferSize)
}

// reads from the underlying reader. Will buffer all input until Regret is called.
func (rb *RegretOnceBuffer) Read(p []byte) (n int, err error) {
	if rb.overflow {
		return rb.reader.Read(p)
	}
	if rb.r < rb.w {
		n = copy(p, rb.buf[rb.r:rb.w])
		rb.r += n
		return
	}
	n, err = rb.reader.Read(p)
	bn := copy(rb.buf[rb.w:], p[:n])
	rb.w, rb.r = rb.w + bn, rb.w + n
	if bn < n {
		rb.overflow = true
	}
	return
}
