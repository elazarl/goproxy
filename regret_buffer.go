package goproxy

import (
	"bytes"
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
	r      io.Reader
	regret bool
	buf    *bytes.Buffer
}

// Same as RegretOnceBuffer, but allows closing the underlying reader
type RegretOnceBufferCloser struct {
	RegretOnceBuffer
	c io.Closer
	closed bool
}

// Closes the underlying readCloser, if we've already closed the
// underlying readCloser, and issued a Regret, we will not close it
// again.
func (rbc *RegretOnceBufferCloser) Close() error {
	if rbc.regret && rbc.closed {
		return nil
	}
	rbc.closed = true
	return rbc.c.Close()
}

func (rbc *RegretOnceBufferCloser) Read(p []byte) (n int, err error) {
	if rbc.regret {
		n, err = rbc.buf.Read(p)
		if err != nil {
			return
		}
	}
	// don't read stream if already closed
	if rbc.regret && rbc.closed {
		return
	}

	en, err := rbc.r.Read(p[n:])
	if !rbc.regret {
		rbc.buf.Write(p[n : n+en])
	}
	n += en
	return
}

// initialize a RegretOnceBufferCloser with underlying readCloser rc
func NewRegretOnceBufferCloser(rc io.ReadCloser) *RegretOnceBufferCloser {
	return &RegretOnceBufferCloser{*NewRegretOnceBuffer(rc), rc, false}
}

// The next read from the RegretOnceBuffer will be as if the underlying reader
// was never read (or from the last point forget is called).
func (rb *RegretOnceBuffer) Regret() {
	if rb.regret == true {
		panic("RegretOnceBuffer was regretted twice")
	}
	rb.regret = true
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
	rb.buf.Reset()
}

// initialize a RegretOnceBuffer with underlying reader r
func NewRegretOnceBuffer(r io.Reader) *RegretOnceBuffer {
	return &RegretOnceBuffer{r: r, regret: false, buf: new(bytes.Buffer)}
}

// reads from the underlying reader. Will buffer all input until Regret is called.
func (rb *RegretOnceBuffer) Read(p []byte) (n int, err error) {
	if rb.regret {
		n, err = rb.buf.Read(p)
		if err != nil {
			return
		}
	}

	en, err := rb.r.Read(p[n:])
	if !rb.regret {
		rb.buf.Write(p[n : n+en])
	}
	n += en
	return
}
