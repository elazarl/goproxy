package har

import (
    "net/http"
    "time"

    "github.com/yx-zero/goproxy-transparent"
)

// ExportFunc is a function type that users can implement to handle exported entries
type ExportFunc func([]Entry)

// Logger implements a HAR logging extension for goproxy
type Logger struct {
    exportFunc      ExportFunc
    exportInterval  time.Duration
    exportThreshold int
    dataCh          chan Entry
}

// LoggerOption is a function type for configuring the Logger
type LoggerOption func(*Logger)

// WithExportInterval sets the interval for automatic exports
func WithExportInterval(d time.Duration) LoggerOption {
    return func(l *Logger) {
        l.exportInterval = d
    }
}

// WithExportCount sets the number of requests after which to export entries
func WithExportThreshold(threshold int) LoggerOption {
    return func(l *Logger) {
        l.exportThreshold = threshold
    }
}

// NewLogger creates a new HAR logger instance
func NewLogger(exportFunc ExportFunc, opts ...LoggerOption) *Logger {
    l := &Logger{
        exportFunc:     exportFunc,
        exportThreshold: 100,    // Default threshold
        exportInterval: 0,       // Default no interval
        dataCh:         make(chan Entry), 
    }
    
    // Apply options
    for _, opt := range opts {
        opt(l)
    }
    
    go l.exportLoop()
    return l
}
// OnRequest handles incoming HTTP requests
func (l *Logger) OnRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
    ctx.UserData = time.Now()
    return req, nil
}

// OnResponse handles HTTP responses
func (l *Logger) OnResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
    if resp == nil || ctx.Req == nil || ctx.UserData == nil {
        return resp
    }
    startTime, ok := ctx.UserData.(time.Time)
    if !ok {
        return resp
    }
    
    entry := Entry{
        StartedDateTime: startTime,
        Time:           time.Since(startTime).Milliseconds(),
        Request:        parseRequest(ctx),
        Response:       parseResponse(ctx),
        Timings: Timings{
            Send:    0,
            Wait:    time.Since(startTime).Milliseconds(),
            Receive: 0,
        },
    }
    entry.fillIPAddress(ctx.Req)
    
    l.dataCh <- entry 
    return resp
}

func (l *Logger) exportLoop() {
   var entries []Entry 
    
   exportIfNeeded := func() {
        if len(entries) > 0 {
            go l.exportFunc(entries)
            entries = nil 
        } 
    } 
    
    var tickerC <-chan time.Time
    if l.exportInterval > 0 {
        ticker := time.NewTicker(l.exportInterval)
        defer ticker.Stop()
        tickerC = ticker.C
    }
    
    for {
        select {
        case entry, ok := <-l.dataCh:
            if !ok {
                exportIfNeeded()
                return
            }
            entries = append(entries, entry)
            if l.exportThreshold > 0 && len(entries) >= l.exportThreshold {
                exportIfNeeded()
            } 
        case <-tickerC:
            exportIfNeeded()
        }
    }
}

func (l *Logger) Stop() {
    close(l.dataCh)
}
