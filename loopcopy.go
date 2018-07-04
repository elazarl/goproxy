package goproxy

import (
	"io"

	"go.uber.org/zap"
)

func passPortion(
	server string,
	dir string,
	in io.Reader,
	out io.Writer,
	buf []byte,
) (n int, done bool, readErr error, writeErr error) {
	zap.L().Debug(
		"start read",
		zap.String("conn", server),
		zap.String("dir", dir),
	)

	n, readErr = in.Read(buf)

	zap.L().Debug(
		"done read",
		zap.String("conn", server),
		zap.String("dir", dir),
		zap.Int("bytes", n),
		zap.Error(readErr),
	)
	if n > 0 {
		zap.L().Debug(
			"start write",
			zap.String("conn", server),
			zap.String("dir", dir),
		)

		_, writeErr = out.Write(buf[:n])

		zap.L().Debug(
			"done write",
			zap.String("conn", server),
			zap.String("dir", dir),
			zap.Int("bytes", n),
			zap.Error(writeErr),
		)

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
	server string,
	dir string,
	out io.Writer,
	in io.Reader,
) (int, error) {
	buf := make([]byte, 16384)
	total := 0

	for {
		n, done, readErr, writeErr := passPortion(server, dir, in, out, buf)
		total += n

		if readErr != nil {
			zap.L().Debug(
				"reading",
				zap.String("conn", server),
				zap.String("dir", dir),
				zap.Bool("done", done),
				zap.Int("bytes", n),
				zap.Int("total", total),
				zap.Error(readErr),
			)
			return total, readErr
		}
		if writeErr != nil {
			zap.L().Error(
				"writing",
				zap.String("conn", server),
				zap.String("dir", dir),
				zap.Bool("done", done),
				zap.Int("bytes", n),
				zap.Int("total", total),
				zap.Error(writeErr),
			)
			// close the whole socket so the reading goroutine will know
			return total, writeErr
		}

		zap.L().Debug(
			"passed",
			zap.String("conn", server),
			zap.String("dir", dir),
			zap.Bool("done", done),
			zap.Int("bytes", n),
			zap.Int("total", total),
		)

		if done {
			return total, nil
		}
	}
}
