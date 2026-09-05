package regretable_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/elazarl/goproxy/regretable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// word is the payload every reader in this file is created with.
const word = "12345678"

// newReader returns a RegretableReader over word.
func newReader() *regretable.Reader {
	buf := new(bytes.Buffer)
	buf.WriteString(word)
	return regretable.NewRegretableReader(buf)
}

// readAll reads the reader to completion and returns its content as a string.
func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}

func TestRegretableRegretAfterPartialRead(t *testing.T) {
	mb := newReader()

	_, err := mb.Read(make([]byte, 5))
	require.NoError(t, err)
	mb.Regret()

	assert.Equal(t, word, readAll(t, mb), "uncommitted read is gone")
}

func TestRegretableRegretAfterEmptyRead(t *testing.T) {
	mb := newReader()

	_, err := mb.Read(make([]byte, 0))
	require.NoError(t, err)
	mb.Regret()

	assert.Equal(t, word, readAll(t, mb), "uncommitted read is gone")
}

func TestRegretableRegretAfterMixedReads(t *testing.T) {
	mb := newReader()

	for _, size := range []int{1, 0, 5} {
		_, err := mb.Read(make([]byte, size))
		require.NoError(t, err, "read of %d bytes", size)
	}
	mb.Regret()

	assert.Equal(t, word, readAll(t, mb), "uncommitted read is gone")
}

func TestRegretableRegretBeforeRead(t *testing.T) {
	mb := newReader()

	// Regretting before reading anything is a no-op, so the five bytes read
	// afterwards are really consumed.
	mb.Regret()
	_, err := mb.Read(make([]byte, 5))
	require.NoError(t, err)

	assert.Equal(t, "678", readAll(t, mb))
}

func TestRegretableRegretAfterFullRead(t *testing.T) {
	mb := newReader()

	// Ask for more bytes than the reader holds, then take them all back.
	_, err := mb.Read(make([]byte, 20))
	require.NoError(t, err)
	mb.Regret()

	assert.Equal(t, word, readAll(t, mb), "uncommitted read is gone")
}

func TestRegretableRegretTwice(t *testing.T) {
	mb := newReader()

	assert.Equal(t, word, readAll(t, mb))
	mb.Regret()
	assert.Equal(t, word, readAll(t, mb))
	mb.Regret()
	assert.Equal(t, word, readAll(t, mb))
}

func TestRegretableCloserSizeRegrets(t *testing.T) {
	buf := new(bytes.Buffer)
	buf.WriteString("123456")
	mb := regretable.NewRegretableReaderCloserSize(io.NopCloser(buf), 3)

	// The reader overflows its 3 byte buffer, so it cannot regret any more.
	_, err := mb.Read(make([]byte, 4))
	require.NoError(t, err)

	assert.PanicsWithValue(t, "regretting after overflow makes no sense", mb.Regret)
}

// closeCounter counts how many times it was closed.
type closeCounter struct {
	r      io.Reader
	closed int
}

func (cc *closeCounter) Read(b []byte) (int, error) {
	return cc.r.Read(b)
}

func (cc *closeCounter) Close() error {
	cc.closed++
	return nil
}

func TestRegretableCloserRegretsClose(t *testing.T) {
	buf := new(bytes.Buffer)
	buf.WriteString(word)
	cc := &closeCounter{r: buf}
	mb := regretable.NewRegretableReaderCloser(cc)

	_, err := mb.Read([]byte{0})
	require.NoError(t, err)
	require.NoError(t, mb.Close())
	assert.Equal(t, 1, cc.closed, "RegretableReaderCloser ignores Close")

	mb.Regret()
	require.NoError(t, mb.Close())
	assert.Equal(t, 2, cc.closed, "RegretableReaderCloser does ignore Close after regret")
	// TODO(elazar): return an error if client issues Close more than once after regret
}
