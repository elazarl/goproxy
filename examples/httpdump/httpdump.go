package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"path"
	"time"

	"github.com/elazarl/goproxy"
)

type FileStream struct {
	path string
	f    *os.File
}

func NewFileStream(path string) *FileStream {
	return &FileStream{path, nil}
}

func (fs *FileStream) Write(b []byte) (nr int, err error) {
	if fs.f == nil {
		fs.f, err = os.Create(fs.path)
		if err != nil {
			return 0, err
		}
	}
	return fs.f.Write(b)
}

func (fs *FileStream) Close() error {
	fmt.Println("Close", fs.path)
	if fs.f == nil {
		return errors.New("FileStream was never written into")
	}
	return fs.f.Close()
}

type Meta struct {
	req      *http.Request
	resp     *http.Response
	err      error
	t        time.Time
	sess     int64
	bodyPath string
	from     string
}

func fprintf(nr *int64, err *error, w io.Writer, pat string, a ...interface{}) {
	if *err != nil {
		return
	}
	var n int
	n, *err = fmt.Fprintf(w, pat, a...)
	*nr += int64(n)
}

func write(nr *int64, err *error, w io.Writer, b []byte) {
	if *err != nil {
		return
	}
	var n int
	n, *err = w.Write(b)
	*nr += int64(n)
}

func (m *Meta) WriteTo(w io.Writer) (nr int64, err error) {
	if m.req != nil {
		fprintf(&nr, &err, w, "Type: request\r\n")
	} else if m.resp != nil {
		fprintf(&nr, &err, w, "Type: response\r\n")
	}
	fprintf(&nr, &err, w, "ReceivedAt: %v\r\n", m.t)
	fprintf(&nr, &err, w, "Session: %d\r\n", m.sess)
	fprintf(&nr, &err, w, "From: %v\r\n", m.from)
	if m.err != nil {
		fprintf(&nr, &err, w, "Error: %v\r\n\r\n", m.err)
	} else if m.req != nil {
		fprintf(&nr, &err, w, "\r\n")
		buf, err2 := httputil.DumpRequest(m.req, false)
		if err2 != nil {
			return nr, err2
		}
		write(&nr, &err, w, buf)
	} else if m.resp != nil {
		fprintf(&nr, &err, w, "\r\n")
		buf, err2 := httputil.DumpResponse(m.resp, false)
		if err2 != nil {
			return nr, err2
		}
		write(&nr, &err, w, buf)
	}
	return
}

type HttpLogger struct {
	path string
	c    chan *Meta
}

func NewLogger(basepath string) (*HttpLogger, error) {
	f, err := os.Create(path.Join(basepath, "log"))
	if err != nil {
		return nil, err
	}
	logger := &HttpLogger{basepath, make(chan *Meta)}
	go func() {
		for m := range logger.c {
			if _, err := m.WriteTo(f); err != nil {
				log.Println("Can't write meta", err)
			}
		}
	}()
	return logger, nil
}

func (logger *HttpLogger) LogResp(resp *http.Response, ctx *goproxy.ProxyCtx) {
	body := path.Join(logger.path, fmt.Sprintf("%d_resp", ctx.Session))
	from := ""
	if ctx.RoundTrip != nil {
		from = ctx.RoundTrip.TCPAddr.String()
	}
	if resp == nil {
		resp = emptyResp
	} else {
		resp.Body = NewTeeReadCloser(resp.Body, NewFileStream(body))
	}
	logger.LogMeta(&Meta{
		resp: resp,
		err:  ctx.Error,
		t:    time.Now(),
		sess: ctx.Session,
		from: from})
}

var emptyResp = &http.Response{}
var emptyReq = &http.Request{}

func (logger *HttpLogger) LogReq(req *http.Request, ctx *goproxy.ProxyCtx) {
	body := path.Join(logger.path, fmt.Sprintf("%d_req", ctx.Session))
	if req == nil {
		req = emptyReq
	} else {
		req.Body = NewTeeReadCloser(req.Body, NewFileStream(body))
	}
	logger.LogMeta(&Meta{
		req:  req,
		err:  ctx.Error,
		t:    time.Now(),
		sess: ctx.Session,
		from: req.RemoteAddr})
}

func (logger *HttpLogger) LogMeta(m *Meta) {
	logger.c <- m
}

type TeeReadCloser struct {
	r io.Reader
	w io.WriteCloser
	c io.Closer
}

func NewTeeReadCloser(r io.ReadCloser, w io.WriteCloser) io.ReadCloser {
	return &TeeReadCloser{io.TeeReader(r, w), w, r}
}

func (t *TeeReadCloser) Read(b []byte) (int, error) {
	return t.r.Read(b)
}

func (t *TeeReadCloser) Close() error {
	err1 := t.c.Close()
	err2 := t.w.Close()
	if err1 == nil && err2 == nil {
		return nil
	}
	if err1 != nil {
		return err2
	}
	return err1
}

func main() {
	verbose := flag.Bool("v", false, "should every proxy request be logged to stdout")
	addr := flag.String("l", ":8080", "on which address should the proxy listen")
	flag.Parse()
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = *verbose
	if err := os.MkdirAll("db", 0755); err != nil {
		log.Fatal("Can't create dir", err)
	}
	logger, err := NewLogger("db")
	if err != nil {
		log.Fatal("can't open log file", err)
	}
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		logger.LogReq(req, ctx)
		return req, nil
	})
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		logger.LogResp(resp, ctx)
		return resp
	})
	log.Fatal(http.ListenAndServe(*addr, proxy))
}
