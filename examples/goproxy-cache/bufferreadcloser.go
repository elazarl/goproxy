package main

import (
	"bytes"
)
	
type BufferReadCloser struct { 
        *bytes.Buffer 
} 

func (closingBuffer *BufferReadCloser) Close() (err error) { 
        //we don't actually have to do anything here, since the buffer is just some data in memory 
        //and the error is initialized to no-error 
        return 
} 