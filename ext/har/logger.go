package har

import (
    "net/http"
    "time"
    "github.com/elazarl/goproxy"
)

// ExportFunc is a function type that users can implement to handle exported entries
type ExportFunc func([]Entry)

// Logger implements a HAR logging extension for goproxy
type Logger struct {
    entries        []Entry
    captureContent bool
    exportFunc      ExportFunc
    exportInterval  time.Duration
    exportThreshold int
    stopCh          chan struct{}
    dataCh          chan Entry
}

// LoggerOption is a function type for configuring the Logger
type LoggerOption func(*Logger)

// WithExportCount sets the number of requests after which to export entries
func WithExportThreshold(threshold int) LoggerOption {
    return func(l *Logger) {
        l.exportThreshold = threshold
    }
}

// NewLogger creates a new HAR logger instance
func NewLogger(exportFunc ExportFunc, opts ...LoggerOption) *Logger {
    l := &Logger{
        entries:        make([]Entry, 0),
        captureContent: true,
        exportFunc:     exportFunc,
        exportThreshold: 100,    // Default threshold
        exportInterval: 0,       // Default no interval
        stopCh:         make(chan struct{}),
    }
    
    // Apply options
    for _, opt := range opts {
        opt(l)
    }
    
    // Calculate buffer size
    bufferSize := 100  // minimum default
    if l.exportInterval > 0 && l.exportThreshold <= 0 {
        // Interval-only mode: need larger buffer
        bufferSize = 10000
    } else if l.exportThreshold > 0 {
        // Using threshold: buffer = threshold + small safety margin
        bufferSize = l.exportThreshold + 100
    }
    
    l.dataCh = make(chan Entry, bufferSize)
    go l.exportLoop()
    return l
}

// OnRequest handles incoming HTTP requests
func (l *Logger) OnRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
    if ctx != nil {
        ctx.UserData = time.Now()
    }
    return req, nil
}

// OnResponse handles HTTP responses
func (l *Logger) OnResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
    if resp == nil || ctx == nil || ctx.Req == nil || ctx.UserData == nil {
        return resp
    }
    startTime, ok := ctx.UserData.(time.Time)
    if !ok {
        return resp
    }
    
    entry := Entry{
        StartedDateTime: startTime,
        Time:           time.Since(startTime).Milliseconds(),
        Request:        ParseRequest(ctx, l.captureContent),
        Response:       ParseResponse(ctx, l.captureContent),
        Timings: Timings{
            Send:    0,
            Wait:    time.Since(startTime).Milliseconds(),
            Receive: 0,
        },
    }
    entry.fillIPAddress(ctx.Req)
    
    select {
    case l.dataCh <- entry:
    default:
        // Log or handle case where channel is full, just in case 
        ctx.Proxy.Logger.Printf("Warning: HAR logger channel is full, dropping entry")
    } 
    
    return resp
}

func (l *Logger) exportLoop() {
    var tickerC <-chan time.Time
    if l.exportInterval > 0 {
        ticker := time.NewTicker(l.exportInterval)
        defer ticker.Stop()
        tickerC = ticker.C
    }
    
    for {
        select {
        case <-l.stopCh:
            if len(l.entries) > 0 {
                l.exportFunc(l.entries)
            }
            return
        case entry := <-l.dataCh:
            l.entries = append(l.entries, entry)
            if l.exportThreshold > 0 && len(l.entries) >= l.exportThreshold {
                l.exportFunc(l.entries)
                l.entries = make([]Entry, 0)
            }
        case <-tickerC:
            if len(l.entries) > 0 {
                l.exportFunc(l.entries)
                l.entries = make([]Entry, 0)
            }
        }
    }
}

// Stop stops the export loop
func (l *Logger) Stop() {
    close(l.stopCh)
}
