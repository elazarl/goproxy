package har


import (
    "encoding/json"
    "net/http"
    "os"
    "sync"
    "time"

    "github.com/elazarl/goproxy"
)

// Logger implements a HAR logging extension for goproxy
type Logger struct {
	mu             sync.Mutex
	har            *Har
	captureContent bool
}

// NewLogger creates a new HAR logger instance
func NewLogger() *Logger {
	return &Logger{
		har: New(),
	}
}

// OnRequest handles incoming HTTP requests
func (l *Logger) OnRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	// Store the start time in context for later use
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

	// Create HAR entry
	entry := Entry{
		StartedDateTime: startTime,
		Time:           time.Since(startTime).Milliseconds(),
		Request:        ParseRequest(ctx.Req, l.captureContent),
		Response:       ParseResponse(resp, l.captureContent),
		Cache:          Cache{},
		Timings: Timings{
			Send:    0,
			Wait:    time.Since(startTime).Milliseconds(),
			Receive: 0,
		},
	}

	// Add server IP
	entry.FillIPAddress(ctx.Req)

	// Add to HAR log thread-safely
	l.mu.Lock()
	l.har.AppendEntry(entry)
	l.mu.Unlock()

	return resp
}

// SetCaptureContent enables or disables request/response body capture
func (l *Logger) SetCaptureContent(capture bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.captureContent = capture
}

// SaveToFile writes the current HAR log to a file
func (l *Logger) SaveToFile(filename string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(l.har)
}

// Clear resets the HAR log
func (l *Logger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.har = New()
}

// GetEntries returns a copy of the current HAR entries
func (l *Logger) GetEntries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := make([]Entry, len(l.har.Log.Entries))
	copy(entries, l.har.Log.Entries)
	return entries
}
