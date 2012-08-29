package goproxy

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"testing"
)

func TestRegretBuffer(t *testing.T) {
	buf := new(bytes.Buffer)
	mb := NewRegretOnceBuffer(buf)
	word := "12345678"
	buf.WriteString(word)

	fivebytes := make([]byte, 5)
	mb.Read(fivebytes)
	mb.Regret()

	s, _ := ioutil.ReadAll(mb)
	if string(s) != word {
		t.Error("Uncommited read is gone", string(s), "expected", word)
	}
}

func TestRegretBufferEmptyRead(t *testing.T) {
	buf := new(bytes.Buffer)
	mb := NewRegretOnceBuffer(buf)
	word := "12345678"
	buf.WriteString(word)

	zero := make([]byte, 0)
	mb.Read(zero)
	mb.Regret()

	s, err := ioutil.ReadAll(mb)
	if string(s) != word {
		t.Error("Uncommited read is gone, actual:", string(s), "expected:", word, "err:", err)
	}
}

func TestRegretBufferAlsoEmptyRead(t *testing.T) {
	buf := new(bytes.Buffer)
	mb := NewRegretOnceBuffer(buf)
	word := "12345678"
	buf.WriteString(word)

	one := make([]byte, 1)
	zero := make([]byte, 0)
	five := make([]byte, 5)
	mb.Read(one)
	mb.Read(zero)
	mb.Read(five)
	mb.Regret()

	s, _ := ioutil.ReadAll(mb)
	if string(s) != word {
		t.Error("Uncommited read is gone", string(s), "expected", word)
	}
}

func TestRegretBufferRegretBeforeRead(t *testing.T) {
	buf := new(bytes.Buffer)
	mb := NewRegretOnceBuffer(buf)
	word := "12345678"
	buf.WriteString(word)

	five := make([]byte, 5)
	mb.Regret()
	mb.Read(five)

	s, err := ioutil.ReadAll(mb)
	if string(s) != "678" {
		t.Error("Uncommited read is gone", string(s), len(string(s)), "expected", "678", len("678"), "err:", err)
	}
}

func TestRegretBufferFullRead(t *testing.T) {
	buf := new(bytes.Buffer)
	mb := NewRegretOnceBuffer(buf)
	word := "12345678"
	buf.WriteString(word)

	twenty := make([]byte, 20)
	mb.Read(twenty)
	mb.Regret()

	s, _ := ioutil.ReadAll(mb)
	if string(s) != word {
		t.Error("Uncommited read is gone", string(s), len(string(s)), "expected", word, len(word))
	}
}

func TestRegretBufferRegretTwice(t *testing.T) {
	buf := new(bytes.Buffer)
	mb := NewRegretOnceBuffer(buf)
	word := "12345678"
	buf.WriteString(word)

	hasPaniced := false
	defer func() {
		if recover() != nil {
			hasPaniced = true
		}
	}()
	mb.Regret()
	mb.Regret()

	if !hasPaniced {
		t.Error("Regretted twice with no panic")
	}
}

type CloseCounter struct {
	r      io.Reader
	closed int
}

func (cc *CloseCounter) Read(b []byte) (int, error) {
	return cc.r.Read(b)
}

func (cc *CloseCounter) Close() error {
	cc.closed++
	return nil
}

func assert(t *testing.T, b bool, msg string) {
	if !b {
		t.Errorf("Assertion Error: %s", msg)
	}
}

func TestRegretBufferCloserEOF(t *testing.T) {
	buf := new(bytes.Buffer)
	cc := &CloseCounter{buf, 0}
	mb := NewRegretOnceBufferCloser(cc)
	word := "123"
	buf.WriteString(word)

	n, err := mb.Read([]byte{0, 1})
	assert(t, n == 2 && err == nil, fmt.Sprint("unregreted read should work ", n, err))
	mb.Close()
	mb.Regret()

	b := make([]byte, 10)
	n, err = mb.Read(b)
	assert(t, bytes.Equal(b[:2], []byte{'1', '2'}),
		"read after regret should return all data until close")
	assert(t, err == nil, fmt.Sprint("valid read return non nil", err))
	n, err = mb.Read(b[2:])
	assert(t, n == 0, "reading after close should be zero length")
	assert(t, err == io.EOF, fmt.Sprint("reading after close should be EOF ", err))
}

func TestRegretBufferCloserRegretsClose(t *testing.T) {
	buf := new(bytes.Buffer)
	cc := &CloseCounter{buf, 0}
	mb := NewRegretOnceBufferCloser(cc)
	word := "12345678"
	buf.WriteString(word)

	mb.Read([]byte{0})
	mb.Close()
	if cc.closed != 1 {
		t.Error("RegretOnceBufferCloser ignores Close")
	}
	mb.Regret()
	mb.Close()
	if cc.closed != 1 {
		t.Error("RegretOnceBufferCloser does not ignore Close after regret")
	}
	// TODO(elazar): return an error if client issues Close more than once after regret
}
