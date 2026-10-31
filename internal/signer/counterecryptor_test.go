package signer_test

import (
	"crypto/rsa"
	"encoding/binary"
	"io"
	"math"
	"math/rand"
	"testing"

	"github.com/elazarl/goproxy/internal/signer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counterEncryptorSeed is the seed shared by every CounterEncryptorRand built
// in this file, so that streams built from the same key are comparable.
var counterEncryptorSeed = []byte("the quick brown fox run over the lazy dog")

// randSeedReader is a deterministic io.Reader, so that RSA key generation
// always produces the same key.
type randSeedReader struct {
	r rand.Rand
}

func (r *randSeedReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = byte(r.r.Int() & 0xFF)
	}
	return len(b), nil
}

// newTestKey generates an RSA key from a deterministic source of randomness.
func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(&randSeedReader{*rand.New(rand.NewSource(0xFF43109))}, 1024)
	require.NoError(t, err)
	return key
}

// newCounterEncryptor returns a CSPRNG built from key. Two CSPRNGs built from
// the same key produce the same stream.
func newCounterEncryptor(t *testing.T, key *rsa.PrivateKey) signer.CounterEncryptorRand {
	t.Helper()
	c, err := signer.NewCounterEncryptorRandFromKey(key, counterEncryptorSeed)
	require.NoError(t, err)
	return c
}

func TestCounterEncDifferentConsecutive(t *testing.T) {
	c := newCounterEncryptor(t, newTestKey(t))

	for i := 0; i < 100*1000; i++ {
		var a, b int64
		require.NoError(t, binary.Read(&c, binary.BigEndian, &a))
		require.NoError(t, binary.Read(&c, binary.BigEndian, &b))
		require.NotEqual(t, a, b, "two consecutive equal int64 at iteration %d", i)
	}
}

func TestCounterEncIdenticalStreams(t *testing.T) {
	key := newTestKey(t)
	c1, c2 := newCounterEncryptor(t, key), newCounterEncryptor(t, key)

	const nOut = 1000

	// Read the first stream in one go...
	out1 := make([]byte, nOut)
	_, err := io.ReadFull(&c1, out1)
	require.NoError(t, err)

	// ...and the second one in chunks of random size.
	out2 := make([]byte, nOut)
	for remaining := out2; len(remaining) > 0; {
		n := min(1+rand.Intn(256), len(remaining))
		n, err := c2.Read(remaining[:n])
		require.NoError(t, err)
		remaining = remaining[n:]
	}

	assert.Equal(t, out1, out2, "identical CSPRNG does not produce the same output")
}

func TestCounterEncStreamHistogram(t *testing.T) {
	c := newCounterEncryptor(t, newTestKey(t))

	const nOut = 100 * 1000
	out := make([]byte, nOut)
	_, err := io.ReadFull(&c, out)
	require.NoError(t, err)

	refHist := make([]int, 512)
	for range nOut {
		refHist[rand.Intn(256)]++
	}
	hist := make([]int, 512)
	for _, b := range out {
		hist[int(b)]++
	}

	// The CSPRNG output should be distributed like the standard PRNG output.
	// The tolerance below is a guesstimate.
	refStdDev, stdDev := stdDev(refHist), stdDev(hist)
	assert.InDelta(t, refStdDev, stdDev, 1,
		"stddev of ref histogram different than regular PRNG")
}

func stdDev(data []int) float64 {
	var sum, sumSqr float64
	for _, h := range data {
		sum += float64(h)
		sumSqr += float64(h) * float64(h)
	}
	n := float64(len(data))
	variance := (sumSqr - ((sum * sum) / n)) / (n - 1)
	return math.Sqrt(variance)
}
