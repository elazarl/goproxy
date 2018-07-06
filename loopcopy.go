package goproxy

import (
	"io"
	"log"
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
	id string,
	logger *log.Logger,
) (int, error) {
	logger.Printf("%s: started")

	buf := make([]byte, 16384)
	total := 0

	for {
		n, done, readErr, writeErr := passPortion(in, out, buf)
		total += n

		if readErr != nil {
			logger.Printf("%s: %d of %d bytes: read error: %v", id, n, total, readErr)
			return total, readErr
		}
		if writeErr != nil {
			logger.Printf("%s: %d of %d bytes: write error: %v", id, n, total, writeErr)
			return total, writeErr
		}

		if done {
			logger.Printf("%s: done, %d bytes", id, total)
			return total, nil
		}
	}
}
