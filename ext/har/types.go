// Original implementation from abourget/goproxy, adapted for use as an extension.
// HAR specification: http://www.softwareishard.com/blog/har-12-spec/
package har

import (
    "bytes"
    "io"
    "io/ioutil"
    "log"
    "net"
    "net/http"
    "net/url"
    "strings"
    "time"
)

var startingEntrySize int = 1000

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
				Version: "12345",
			},
			Pages:   make([]Page, 0, 10),
			Entries: makeNewEntries(),
		},
	}
	return har
}

func (har *Har) AppendEntry(entry ...Entry) {
	har.Log.Entries = append(har.Log.Entries, entry...)
}

func (har *Har) AppendPage(page ...Page) {
	har.Log.Pages = append(har.Log.Pages, page...)
}

func makeNewEntries() []Entry {
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

func ParseRequest(req *http.Request, captureContent bool) *Request {
	if req == nil {
		return nil
	}
	harRequest := Request{
		Method:      req.Method,
		Url:         req.URL.String(),
		HttpVersion: req.Proto,
		Cookies:     parseCookies(req.Cookies()),
		Headers:     parseStringArrMap(req.Header),
		QueryString: parseStringArrMap((req.URL.Query())),
		BodySize:    req.ContentLength,
		HeadersSize: calcHeaderSize(req.Header),
	}

	if captureContent && (req.Method == "POST" || req.Method == "PUT") {
		harRequest.PostData = parsePostData(req)
	}

	return &harRequest
}

func (harEntry *Entry) FillIPAddress(req *http.Request) {
	host, _, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		host = req.URL.Host
	}
	if ip := net.ParseIP(host); ip != nil {
		harEntry.ServerIpAddress = string(ip)
	}

	if ipaddr, err := net.LookupIP(host); err == nil {
		for _, ip := range ipaddr {
			if ip.To4() != nil {
				harEntry.ServerIpAddress = ip.String()
				return
			}
		}
	}
}

func calcHeaderSize(header http.Header) int64 {
	headerSize := 0
	for headerName, headerValues := range header {
		headerSize += len(headerName) + 2
		for _, v := range headerValues {
			headerSize += len(v)
		}
	}
	return int64(headerSize)
}

func parsePostData(req *http.Request) *PostData {
	defer func() {
		if e := recover(); e != nil {
			log.Printf("Error parsing request to %v: %v\n", req.URL, e)
		}
	}()

	harPostData := new(PostData)
	contentType := req.Header["Content-Type"]
	if contentType == nil {
		panic("Missing content type in request")
	}
	harPostData.MimeType = contentType[0]

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
	} else {
		str, _ := ioutil.ReadAll(req.Body)
		harPostData.Text = string(str)
	}
	return harPostData
}

func parseStringArrMap(stringArrMap map[string][]string) []NameValuePair {
	index := 0
	harQueryString := make([]NameValuePair, len(stringArrMap))
	for k, v := range stringArrMap {
		escapedKey, _ := url.QueryUnescape(k)
		escapedValues, _ := url.QueryUnescape(strings.Join(v, ","))
		harNameValuePair := NameValuePair{
			Name:  escapedKey,
			Value: escapedValues,
		}
		harQueryString[index] = harNameValuePair
		index++
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

func ParseResponse(resp *http.Response, captureContent bool) *Response {
    if resp == nil {
        return nil
    }

    statusText := resp.Status
    if len(resp.Status) > 4 {
        statusText = resp.Status[4:]
    }
    redirectURL := resp.Header.Get("Location")
    harResponse := Response{
        Status:      resp.StatusCode,
        StatusText:  statusText,
        HttpVersion: resp.Proto,
        Cookies:     parseCookies(resp.Cookies()),
        Headers:     parseStringArrMap(resp.Header),
        RedirectUrl: redirectURL,
        BodySize:    resp.ContentLength,
        HeadersSize: calcHeaderSize(resp.Header),
    }

    if captureContent && resp.Body != nil {
        body, err := io.ReadAll(resp.Body)
        if err != nil {
            log.Printf("Error reading response body: %v", err)
            return &harResponse
        }
        // Create a new reader for the response body
        resp.Body = io.NopCloser(bytes.NewBuffer(body))
        
        harResponse.Content = Content{
            Size:     len(body),
            Text:     string(body),
            MimeType: resp.Header.Get("Content-Type"),
        }
    }

    return &harResponse
}

func parseContent(resp *http.Response, harContent *Content) {
	defer func() {
		if e := recover(); e != nil {
			log.Printf("Error parsing response to %v: %v\n", resp.Request.URL, e)
		}
	}()

	contentType := resp.Header["Content-Type"]
	if contentType == nil {
		panic("Missing content type in response")
	}
	harContent.MimeType = contentType[0]
	if resp.ContentLength == 0 {
		log.Println("Empty content")
		return
	}

	body, _ := ioutil.ReadAll(resp.Body)
	harContent.Text = string(body)
	harContent.Size = len(body)
	return
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
