package goproxy

import (
	"io"
	"log"
	"sync"
)

type incomingConn interface {
	Read(b []byte) (n int, err error)
	Write(b []byte) (n int, err error)
	Close() error
}

type outgoingConn interface {
	Read(b []byte) (n int, err error)
	Write(b []byte) (n int, err error)
	CloseWrite() error
	Close() error
}

func pipeInOut(
	incoming incomingConn,
	outgoing outgoingConn,
	id string,
	logger *log.Logger,
) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go outgoingReadLoop(incoming, outgoing, id, logger, wg)

	if incomingReadLoop(incoming, outgoing, id, logger) {
		outgoing.CloseWrite()
		wg.Wait()
	}

	outgoing.Close()
	incoming.Close()
}

func incomingReadLoop(
	incoming incomingConn,
	outgoing outgoingConn,
	id string,
	logger *log.Logger,
) bool {
	buf := make([]byte, 16384)

	for {
		n, err := incoming.Read(buf)
		if err != nil {
			if err != io.EOF {
				logger.Printf("%s: error reading from incoming: %d bytes, %v", id, n, err)
				return false
			}

			return true
		}

		n, err = outgoing.Write(buf[:n])
		if err != nil {
			logger.Printf("%s: error writing to outgoing: %d bytes, %v", id, n, err)
			return false
		}
	}
}

func outgoingReadLoop(
	incoming incomingConn,
	outgoing outgoingConn,
	id string,
	logger *log.Logger,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	buf := make([]byte, 16384)

	for {
		n, err := outgoing.Read(buf)
		if err != nil {
			if err != io.EOF {
				logger.Printf("%s: error reading from outgoing: %d bytes, %v", id, n, err)
			}

			break
		}

		n, err = incoming.Write(buf[:n])
		if err != nil {
			logger.Printf("%s: error writing to incoming: %d bytes, %v", id, n, err)

			break
		}
	}

	incoming.Close()
}
