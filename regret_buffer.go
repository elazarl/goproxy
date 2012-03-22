package goproxy

import ("bytes"
	"io")

type RegretOnceBuffer struct {
	r      io.Reader
	regret bool
	buf    *bytes.Buffer
}

type RegretOnceBufferCloser struct {
	RegretOnceBuffer
	c io.Closer
}

func (rbc *RegretOnceBufferCloser) Close() error {
	return rbc.c.Close()
}

func NewRegretOnceBufferCloser(rc io.ReadCloser) *RegretOnceBufferCloser {
	return &RegretOnceBufferCloser{*NewRegretOnceBuffer(rc),rc}
}

func (rb *RegretOnceBuffer) Regret() {
	if rb.regret == true {
		panic("RegretOnceBuffer was regretted twice")
	}
	rb.regret = true
}
func (rb *RegretOnceBuffer) Forget() {
	rb.buf.Reset()
}

func NewRegretOnceBuffer(r io.Reader) *RegretOnceBuffer {
	return &RegretOnceBuffer{r:r,regret:false,buf:new(bytes.Buffer)}
}

func (rb *RegretOnceBuffer) Read(p []byte) (n int, err error) {
	if rb.regret {
		n,err = rb.buf.Read(p[:rb.buf.Len()])
		if err != nil {
			return
		}
	}

	en,err := rb.r.Read(p[n:])
	if ! rb.regret {
		rb.buf.Write(p[n:n+en])
	}
	n += en
	return
}
