package goproxy

import (
	"io"
)

func passPortion(
	in io.Reader,
	out io.Writer,
	buf []byte,
) (n int, done bool, readErr error, writeErr error) {
	n, readErr = in.Read(buf)

	if n > 0 {
		_, writeErr = out.Write(buf[:n])
		if writeErr != nil {
			n = 0
			return
		}
	}

	if readErr != nil {
		if readErr == io.EOF {
			done = true
			readErr = nil
			return
		}

		return
	}

	return
}

func loopCopy(
	out io.Writer,
	in io.Reader,
) (int, error) {
	buf := make([]byte, 16384)
	total := 0

	for {
		n, done, readErr, writeErr := passPortion(in, out, buf)
		total += n

		if readErr != nil {
			return total, readErr
		}
		if writeErr != nil {
			return total, writeErr
		}

		if done {
			return total, nil
		}
	}
}
