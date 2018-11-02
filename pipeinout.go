package goproxy

import (
	"io"
	"log"
	"sync"
	"time"
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
	sTime := time.Now()
	buf := make([]byte, 16384)
	total := 0

	for {
		n, err := incoming.Read(buf)
		if err != nil {
			if err != io.EOF {
				logger.Printf("%s: error reading from incoming: %d/%d bytes, %f seconds, %v", id, total, n, time.Now().Sub(sTime).Seconds(), err)
				return false
			}

			return true
		}

		total += n

		n, err = outgoing.Write(buf[:n])
		if err != nil {
			logger.Printf("%s: error writing to outgoing: %d/%d bytes, %f seconds, %v", id, total, n, time.Now().Sub(sTime).Seconds(), err)
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

	sTime := time.Now()
	buf := make([]byte, 16384)
	total := 0

	logger.Printf("%s: connect out: started", id)

	for {
		n, err := outgoing.Read(buf)
		if err != nil {
			if err != io.EOF {
				logger.Printf("%s: error reading from outgoing: %d/%d bytes, %f seconds, %v", id, total, n, time.Now().Sub(sTime).Seconds(), err)
			}

			break
		}

		total += n

		n, err = incoming.Write(buf[:n])
		if err != nil {
			logger.Printf("%s: error writing to incoming: %d/%d bytes, %f seconds, %v", id, total, n, time.Now().Sub(sTime).Seconds(), err)

			break
		}
	}

	incoming.Close()

	logger.Printf("%s: connect out: done, %d bytes, %f seconds", id, total, time.Now().Sub(sTime).Seconds())
}
