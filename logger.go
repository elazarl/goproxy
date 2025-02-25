package goproxy

import (
	"log"
	"os"
	"strconv"
)

// Logger is implemented by any type that can log the proxy server events.
type Logger interface {
	Errorf(sessionID int64, format string, values ...any)
	Warnf(sessionID int64, format string, values ...any)
	Infof(sessionID int64, format string, values ...any)
	Debugf(sessionID int64, format string, values ...any)
}

// Errorf logs an ERROR message to the logger specified in proxy options.
// It can also be called from a request handler.
// Your custom logger can handle its own log level and skip the message if it's not needed,
// take a look to the default logger for a reference implementation.
//
//	proxy.OnRequest().DoFunc(func(r *http.Request,ctx *goproxy.ProxyCtx) (*http.Request, *http.Response){
//		f,err := os.OpenFile(cachedContent)
//		if err != nil {
//			ctx.Options.Warnf("error open file %v: %v",cachedContent,err)
//			return r, nil
//		}
//		return r, nil
//	})
func (opt Options) Errorf(ctx *ProxyCtx, format string, values ...any) {
	if opt.Logger == nil {
		return
	}
	opt.Logger.Errorf(ctx.SessionID, format, values...)
}

// Infof logs an INFO message to the logger specified in proxy options.
func (opt Options) Infof(ctx *ProxyCtx, format string, values ...any) {
	if opt.Logger == nil {
		return
	}
	opt.Logger.Infof(ctx.SessionID, format, values...)
}

// Warnf logs a WARNING message to the logger specified in proxy options.
func (opt Options) Warnf(ctx *ProxyCtx, format string, values ...any) {
	if opt.Logger == nil {
		return
	}
	opt.Logger.Warnf(ctx.SessionID, format, values...)
}

// Debugf logs a DEBUG message to the logger specified in proxy options.
func (opt Options) Debugf(ctx *ProxyCtx, format string, values ...any) {
	if opt.Logger == nil {
		return
	}
	opt.Logger.Debugf(ctx.SessionID, format, values...)
}

type LoggingLevel int

const (
	DEBUG LoggingLevel = iota
	INFO
	WARNING
	ERROR
)

type DefaultLogger struct {
	*log.Logger
	level LoggingLevel
}

func NewDefaultLogger(level LoggingLevel) *DefaultLogger {
	return &DefaultLogger{
		Logger: log.New(os.Stderr, "goproxy ", log.LstdFlags),
		level:  level,
	}
}

func (l *DefaultLogger) Errorf(sessionID int64, format string, values ...any) {
	if l.level <= ERROR {
		l.Printf("ERROR: "+l.formatSessionID(sessionID, format), values...)
	}
}

func (l *DefaultLogger) Warnf(sessionID int64, format string, values ...any) {
	if l.level <= WARNING {
		l.Printf("WARNING: "+l.formatSessionID(sessionID, format), values...)
	}
}

func (l *DefaultLogger) Infof(sessionID int64, format string, values ...any) {
	if l.level <= INFO {
		l.Printf("INFO: "+l.formatSessionID(sessionID, format), values...)
	}
}

func (l *DefaultLogger) Debugf(sessionID int64, format string, values ...any) {
	if l.level <= DEBUG {
		l.Printf("DEBUG: "+l.formatSessionID(sessionID, format), values...)
	}
}

func (l *DefaultLogger) formatSessionID(sessionID int64, format string) string {
	formattedSessionID := strconv.FormatInt(sessionID&0xFFFF, 10)
	return "[" + formattedSessionID + "] " + format
}
