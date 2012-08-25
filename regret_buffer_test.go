package goproxy

import (
	"bytes"
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

	s, _ := ioutil.ReadAll(mb)
	if string(s) != word {
		t.Error("Uncommited read is gone", string(s), "expected", word)
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

	s, _ := ioutil.ReadAll(mb)
	if string(s) != "678" {
		t.Error("Uncommited read is gone", string(s), len(string(s)), "expected", "678", len("678"))
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
	r io.Reader
	closed int
}

func (cc *CloseCounter) Read(b []byte) (int, error) {
	return cc.r.Read(b)
}

func (cc *CloseCounter) Close() error {
	cc.closed++
	return nil
}

func TestRegretBufferCloserRegretsClose(t *testing.T) {
	buf := new(bytes.Buffer)
	cc := &CloseCounter{buf, 0}
	mb := NewRegretOnceBufferCloser(cc)
	word := "12345678"
	buf.WriteString(word)

	mb.Read([]byte{0})
	mb.Close()
	if cc.closed!=1 {
		t.Error("RegretOnceBufferCloser ignores Close")
	}
	mb.Regret()
	mb.Close()
	if cc.closed!=1 {
		t.Error("RegretOnceBufferCloser does not ignore Close after regret")
	}
	// TODO(elazar): return an error if client issues Close more than once after regret
}
