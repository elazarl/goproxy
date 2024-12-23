package signer_test

import (
	"bytes"
	"crypto/rsa"
	"encoding/binary"
	"io"
	"math"
	"math/rand"
	"testing"

	"github.com/elazarl/goproxy/internal/signer"
)

type RandSeedReader struct {
	r rand.Rand
}

func (r *RandSeedReader) Read(b []byte) (n int, err error) {
	for i := range b {
		b[i] = byte(r.r.Int() & 0xFF)
	}
	return len(b), nil
}

func fatalOnErr(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatal(msg, err)
	}
}

func TestCounterEncDifferentConsecutive(t *testing.T) {
	k, err := rsa.GenerateKey(&RandSeedReader{*rand.New(rand.NewSource(0xFF43109))}, 128)
	fatalOnErr(t, err, "rsa.GenerateKey")
	c, err := signer.NewCounterEncryptorRandFromKey(k, []byte("the quick brown fox run over the lazy dog"))
	fatalOnErr(t, err, "NewCounterEncryptorRandFromKey")
	for i := 0; i < 100*1000; i++ {
		var a, b int64
		fatalOnErr(t, binary.Read(&c, binary.BigEndian, &a), "read a")
		fatalOnErr(t, binary.Read(&c, binary.BigEndian, &b), "read b")
		if a == b {
			t.Fatal("two consecutive equal int64", a, b)
		}
	}
}

func TestCounterEncIdenticalStreams(t *testing.T) {
	k, err := rsa.GenerateKey(&RandSeedReader{*rand.New(rand.NewSource(0xFF43109))}, 128)
	fatalOnErr(t, err, "rsa.GenerateKey")
	c1, err := signer.NewCounterEncryptorRandFromKey(k, []byte("the quick brown fox run over the lazy dog"))
	fatalOnErr(t, err, "NewCounterEncryptorRandFromKey")
	c2, err := signer.NewCounterEncryptorRandFromKey(k, []byte("the quick brown fox run over the lazy dog"))
	fatalOnErr(t, err, "NewCounterEncryptorRandFromKey")
	const nOut = 1000
	out1, out2 := make([]byte, nOut), make([]byte, nOut)
	_, _ = io.ReadFull(&c1, out1)
	tmp := out2
	for len(tmp) > 0 {
		n := 1 + rand.Intn(256)
		if n > len(tmp) {
			n = len(tmp)
		}
		n, err := c2.Read(tmp[:n])
		fatalOnErr(t, err, "CounterEncryptorRand.Read")
		tmp = tmp[n:]
	}
	if !bytes.Equal(out1, out2) {
		t.Error("identical CSPRNG does not produce the same output")
	}
}

func stddev(data []int) float64 {
	var sum, sumSqr float64 = 0, 0
	for _, h := range data {
		sum += float64(h)
		sumSqr += float64(h) * float64(h)
	}
	n := float64(len(data))
	variance := (sumSqr - ((sum * sum) / n)) / (n - 1)
	return math.Sqrt(variance)
}

func TestCounterEncStreamHistogram(t *testing.T) {
	k, err := rsa.GenerateKey(&RandSeedReader{*rand.New(rand.NewSource(0xFF43109))}, 128)
	fatalOnErr(t, err, "rsa.GenerateKey")
	c, err := signer.NewCounterEncryptorRandFromKey(k, []byte("the quick brown fox run over the lazy dog"))
	fatalOnErr(t, err, "NewCounterEncryptorRandFromKey")
	nout := 100 * 1000
	out := make([]byte, nout)
	_, _ = io.ReadFull(&c, out)
	refhist := make([]int, 512)
	for i := 0; i < nout; i++ {
		refhist[rand.Intn(256)]++
	}
	hist := make([]int, 512)
	for _, b := range out {
		hist[int(b)]++
	}
	refstddev, stddev := stddev(refhist), stddev(hist)
	// due to lack of time, I guestimate
	t.Logf("ref:%v - act:%v = %v", refstddev, stddev, math.Abs(refstddev-stddev))
	if math.Abs(refstddev-stddev) >= 1 {
		t.Errorf("stddev of ref histogram different than regular PRNG: %v %v", refstddev, stddev)
	}
}
