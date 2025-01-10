// Original implementation from abourget/goproxy, adapted for use as an extension.
// HAR specification: http://www.softwareishard.com/blog/har-12-spec/
package har

import (
    "bytes"
    "io"
    "net/http"
    "net/url"
    "mime"
    "net"
    "strings"
    "time"

    "github.com/elazarl/goproxy"
)

type Har struct {
	Log Log `json:"log"`
}

type Log struct {
	Version string   `json:"version"`
	Creator Creator  `json:"creator"`
	Browser *Browser `json:"browser,omitempty"`
	Pages   []Page   `json:"pages,omitempty"`
	Entries []Entry  `json:"entries"`
	Comment string   `json:"comment,omitempty"`
}

func New() *Har {
	har := &Har{
		Log: Log{
			Version: "1.2",
			Creator: Creator{
				Name:    "GoProxy",
				Version: "1.0",
			},
			Pages:   make([]Page, 0, 10),
			Entries: makeNewEntries(),
		},
	}
	return har
}

func makeNewEntries() []Entry {
    const startingEntrySize int = 1000
	return make([]Entry, 0, startingEntrySize)
}

type Creator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Comment string `json:"comment,omitempty"`
}

type Browser struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Comment string `json:"comment,omitempty"`
}

type Page struct {
	ID              string      `json:"id,omitempty"`
	StartedDateTime time.Time   `json:"startedDateTime"`
	Title           string      `json:"title"`
	PageTimings     PageTimings `json:"pageTimings"`
	Comment         string      `json:"comment,omitempty"`
}

type Entry struct {
	PageRef         string    `json:"pageref,omitempty"`
	StartedDateTime time.Time `json:"startedDateTime"`
	Time            int64     `json:"time"`
	Request         *Request  `json:"request"`
	Response        *Response `json:"response"`
	Cache           Cache     `json:"cache"`
	Timings         Timings   `json:"timings"`
	ServerIpAddress string    `json:"serverIpAddress,omitempty"`
	Connection      string    `json:"connection,omitempty"`
	Comment         string    `json:"comment,omitempty"`
}

type Cache struct {
	BeforeRequest *CacheEntry `json:"beforeRequest,omitempty"`
	AfterRequest  *CacheEntry `json:"afterRequest,omitempty"`
}

type CacheEntry struct {
	Expires    string `json:"expires,omitempty"`
	LastAccess string `json:"lastAccess"`
	ETag       string `json:"eTag"`
	HitCount   int    `json:"hitCount"`
	Comment    string `json:"comment,omitempty"`
}

type Request struct {
	Method      string          `json:"method"`
	Url         string          `json:"url"`
	HttpVersion string          `json:"httpVersion"`
	Cookies     []Cookie        `json:"cookies"`
	Headers     []NameValuePair `json:"headers"`
	QueryString []NameValuePair `json:"queryString"`
	PostData    *PostData       `json:"postData,omitempty"`
	BodySize    int64           `json:"bodySize"`
	HeadersSize int64           `json:"headersSize"`
}

func (entry *Entry) fillIPAddress(req *http.Request) {
    host := req.URL.Hostname()
    
    // try to parse the host as an IP address
    if ip := net.ParseIP(host); ip != nil {
        entry.ServerIpAddress = ip.String()
        return
    } 
}

// Shared utility function for reading body content
func readBody(ctx *goproxy.ProxyCtx, body io.ReadCloser) ([]byte, error) {
    content, err := io.ReadAll(body)
    if err != nil {
        ctx.Proxy.Logger.Printf("Error reading body: %v", err)
        return nil, err
    }
    return content, nil
}

// Shared function for handling mime types
func parseMediaType(ctx *goproxy.ProxyCtx, header http.Header) string {
    contentType := header.Get("Content-Type")
    if contentType == "" {
        return ""
    }
    
    mediaType, _, err := mime.ParseMediaType(contentType)
    if err != nil {
        ctx.Proxy.Logger.Printf("Error parsing media type: %v", err)
        return ""
    }
    return mediaType
}

func parsePostData(ctx *goproxy.ProxyCtx, req *http.Request) *PostData {
    mediaType := parseMediaType(ctx, req.Header)
    if mediaType == "" {
        return nil
    }
    
    harPostData := &PostData{
        MimeType: mediaType,
    } 

    if err := req.ParseForm(); err != nil {
        ctx.Proxy.Logger.Printf("Error parsing form: %v", err)
        return nil
    }
    
    if len(req.PostForm) > 0 {
        for k, vals := range req.PostForm {
            for _, v := range vals {
                param := PostDataParam{
                    Name:  k,
                    Value: v,
                }
                harPostData.Params = append(harPostData.Params, param)
            }
        }
    } else if body, err := readBody(ctx, req.Body); err == nil {
        req.Body = io.NopCloser(bytes.NewBuffer(body))
        harPostData.Text = string(body)
    }
    
    return harPostData
}

type Response struct {
	Status      int             `json:"status"`
	StatusText  string          `json:"statusText"`
	HttpVersion string          `json:"httpVersion"`
	Cookies     []Cookie        `json:"cookies"`
	Headers     []NameValuePair `json:"headers"`
	Content     Content         `json:"content"`
	RedirectUrl string          `json:"redirectURL"`
	BodySize    int64           `json:"bodySize"`
	HeadersSize int64           `json:"headersSize"`
	Comment     string          `json:"comment,omitempty"`
}

func parseResponse(ctx *goproxy.ProxyCtx) *Response {
    if ctx.Resp == nil {
        return nil
    } 

    resp := ctx.Resp
    harResponse := Response{
        Status:      resp.StatusCode,
        StatusText:  http.StatusText(resp.StatusCode),
        HttpVersion: resp.Proto,
        Cookies:     parseCookies(resp.Cookies()),
        Headers:     parseStringArrMap(resp.Header),
        RedirectUrl: resp.Header.Get("Location"),
        BodySize:    resp.ContentLength,
        HeadersSize: -1,
    }

    if resp.Body == nil {
        return &harResponse
    }

    body, err := readBody(ctx, resp.Body)
    if err != nil {
        return &harResponse
    }

    resp.Body = io.NopCloser(bytes.NewBuffer(body))
    harResponse.Content = Content{
        Size:     len(body),
        Text:     string(body),
        MimeType: parseMediaType(ctx, resp.Header),
    }

    return &harResponse
}

func parseRequest(ctx *goproxy.ProxyCtx) *Request {
    if ctx.Req == nil {
        ctx.Proxy.Logger.Printf("ParseRequest: nil request")
        return nil
    }
    
    req := ctx.Req
    harRequest := &Request{
        Method:      req.Method,
        Url:         req.URL.String(),
        HttpVersion: req.Proto,
        Cookies:     parseCookies(req.Cookies()),
        Headers:     parseStringArrMap(req.Header),
        QueryString: parseStringArrMap(req.URL.Query()),
        BodySize:    req.ContentLength,
        HeadersSize: -1,
    }
    
    if req.Method != http.MethodPost && req.Method != http.MethodPut {
        return harRequest
    }

    ctx.Proxy.Logger.Printf("ParseRequest: creating PostData, hasBody=%v, hasGetBody=%v", 
        req.Body != nil, req.GetBody != nil)
        
    if postData := parsePostData(ctx, req); postData != nil {
        harRequest.PostData = postData
    }

    return harRequest
}

func parseStringArrMap(stringArrMap map[string][]string) []NameValuePair {
	harQueryString := make([]NameValuePair, 0, len(stringArrMap))
	
	for k, v := range stringArrMap {
		escapedKey, err := url.QueryUnescape(k)
		if err != nil {
			// Use original key if unescaping fails
			escapedKey = k
		}

		escapedValues, err := url.QueryUnescape(strings.Join(v, ","))
		if err != nil {
			// Use original joined values if unescaping fails
			escapedValues = strings.Join(v, ",")
		}

		harNameValuePair := NameValuePair{
			Name:  escapedKey,
			Value: escapedValues,
		}
		
		harQueryString = append(harQueryString, harNameValuePair)
	}
	
	return harQueryString
}

func parseCookies(cookies []*http.Cookie) []Cookie {
	harCookies := make([]Cookie, len(cookies))
	for i, cookie := range cookies {
		harCookie := Cookie{
			Name:     cookie.Name,
			Domain:   cookie.Domain,
			HttpOnly: cookie.HttpOnly,
			Path:     cookie.Path,
			Secure:   cookie.Secure,
			Value:    cookie.Value,
		}
		if !cookie.Expires.IsZero() {
			harCookie.Expires = &cookie.Expires
		}
		harCookies[i] = harCookie
	}
	return harCookies
}

type Cookie struct {
	Name     string     `json:"name"`
	Value    string     `json:"value"`
	Path     string     `json:"path,omitempty"`
	Domain   string     `json:"domain,omitempty"`
	Expires  *time.Time `json:"expires,omitempty"`
	HttpOnly bool       `json:"httpOnly,omitempty"`
	Secure   bool       `json:"secure,omitempty"`
}

type NameValuePair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type PostData struct {
	MimeType string          `json:"mimeType"`
	Params   []PostDataParam `json:"params,omitempty"`
	Text     string          `json:"text,omitempty"`
	Comment  string          `json:"comment,omitempty"`
}

type PostDataParam struct {
	Name        string `json:"name"`
	Value       string `json:"value,omitempty"`
	FileName    string `json:"fileName,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

type Content struct {
	Size        int    `json:"size"`
	Compression int    `json:"compression,omitempty"`
	MimeType    string `json:"mimeType"`
	Text        string `json:"text,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

type PageTimings struct {
	OnContentLoad int64  `json:"onContentLoad"`
	OnLoad        int64  `json:"onLoad"`
	Comment       string `json:"comment,omitempty"`
}

type Timings struct {
	Dns     int64  `json:"dns,omitempty"`
	Blocked int64  `json:"blocked,omitempty"`
	Connect int64  `json:"connect,omitempty"`
	Send    int64  `json:"send"`
	Wait    int64  `json:"wait"`
	Receive int64  `json:"receive"`
	Ssl     int64  `json:"ssl,omitempty"`
	Comment string `json:"comment,omitempty"`
}
