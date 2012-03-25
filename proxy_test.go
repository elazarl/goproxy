package goproxy

import "bufio"
import "bytes"
import "strings"
import "net"
import "encoding/base64"
import "crypto/tls"
import "image"
import "image/png"
import "io"
import "io/ioutil"
import "net/http"
import "net/http/httptest"
import "net/url"
import "os"
import "testing"

var _ = bufio.ErrBufferFull
var _ = base64.StdEncoding
var _ = net.FlagUp

var acceptAllCerts = &tls.Config{InsecureSkipVerify: true}

var noProxyClient = &http.Client{Transport: &http.Transport{TLSClientConfig: acceptAllCerts}}

var https = httptest.NewTLSServer(nil)
var srv  = httptest.NewServer(nil)
var fs    = httptest.NewServer(http.FileServer(http.Dir(".")))


func init() {
	http.DefaultServeMux.Handle("/bobo", ConstantHanlder("bobo"))
}

type ConstantHanlder string

func (h ConstantHanlder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, string(h))
}

func get(url string, client *http.Client) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	txt, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return nil, err
	}
	return txt, nil
}

func getOrFail(url string, client *http.Client, t *testing.T) []byte {
	txt, err := get(url, client)
	if err != nil {
		t.Fatal("Can't fetch url", url, err)
	}
	return txt
}
func localFile(url string) string { return fs.URL + "/" + url }
func localTls(url string) string  { return https.URL + url }

func TestSimpleHttpReqWithProxy(t *testing.T) {
	client, _, s := oneShotProxy(t)
	defer s.Close()

	if r := string(getOrFail(srv.URL+"/bobo", client,t)); r != "bobo" {
		t.Error("proxy server does not serve constant handlers", r)
	}
	if r := string(getOrFail(srv.URL+"/bobo", client,t)); r != "bobo" {
		t.Error("proxy server does not serve constant handlers", r)
	}

	if string(getOrFail(https.URL+"/bobo", client, t)) != "bobo" {
		t.Error("TLS server does not serve constant handlers, when proxy is used")
	}
}


func oneShotProxy(t *testing.T) (client *http.Client, proxy *ProxyHttpServer, s *httptest.Server) {
	proxy = NewProxyHttpServer()
	s = httptest.NewServer(proxy)

	proxyUrl, _ := url.Parse(s.URL)
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl)}
	client = &http.Client{Transport: tr}
	return
}

func TestSimpleHook(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	defer l.Close()

	proxy.OnRequest(SrcIpIs("127.0.0.1")).DoFunc(func(req *http.Request, ctx *ProxyCtx) (*http.Request,*http.Response) {
		req.URL.Path = "/bobo"
		return req,nil
	})
	if result := string(getOrFail(srv.URL+("/momo"), client, t)); result != "bobo" {
		t.Error("Redirecting all requests from 127.0.0.1 to bobo, didn't work." +
			" (Might break if Go's client sets RemoteAddr to IPv6 address). Got: " +
			result)
	}
}

func TestAlwaysHook(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	defer l.Close()

	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *ProxyCtx) (*http.Request,*http.Response) {
		req.URL.Path = "/bobo"
		return req,nil
	})
	if result := string(getOrFail(srv.URL+("/momo"), client, t)); result != "bobo" {
		t.Error("Redirecting all requests from 127.0.0.1 to bobo, didn't work." +
			" (Might break if Go's client sets RemoteAddr to IPv6 address). Got: " +
			result)
	}
}

func TestReplaceResponse(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	defer l.Close()

	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
		resp.StatusCode = http.StatusOK
		resp.Body = ioutil.NopCloser(bytes.NewBufferString("chico"))
		return resp
	})

	if result := string(getOrFail(srv.URL+("/momo"), client, t)); result != "chico" {
		t.Error("hooked response, should be chico, instead:", result)
	}
}

func TestReplaceReponseForUrl(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	defer l.Close()

	proxy.OnResponse(UrlIs("/koko")).DoFunc(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
		resp.StatusCode = http.StatusOK
		resp.Body = ioutil.NopCloser(bytes.NewBufferString("chico"))
		return resp
	})

	if result := string(getOrFail(srv.URL+("/koko"), client,t)); result != "chico" {
		t.Error("hooked 'koko', should be chico, instead:", result)
	}
	if result := string(getOrFail(srv.URL+("/bobo"), client,t)); result != "bobo" {
		t.Error("still, bobo should stay as usual, instead:", result)
	}
}

func TestOneShotFileServer(t *testing.T) {
	client, _, l := oneShotProxy(t)
	defer l.Close()

	file := "test_data/panda.png"
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal("Cannot find", file)
	}
	if resp, err := client.Get(fs.URL + "/" + file); err == nil {
		b,err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatal("got",string(b))
		}
		if int64(len(b)) != info.Size() {
			t.Error("Expected Length", file, info.Size(), "actually", len(b), "starts", string(b[:10]))
		}
	} else {
		t.Fatal("Cannot read from fs server", err)
	}
}

func TestContentType(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	defer l.Close()

	proxy.OnResponse(ContentTypeIs("image/png")).DoFunc(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
		resp.Header.Set("X-Shmoopi", "1")
		return resp
	})

	for _, file := range []string{"test_data/panda.png", "test_data/football.png"} {
		if resp, err := client.Get(localFile(file)); err != nil || resp.Header.Get("X-Shmoopi") != "1" {
			if err == nil {
				t.Error("pngs should have X-Shmoopi header = 1, actually", resp.Header.Get("X-Shmoopi"))
			} else {
				t.Error("error reading png", err)
			}
		}
	}

	file := "baby.jpg"
	if resp, err := client.Get(localFile(file)); err != nil || resp.Header.Get("X-Shmoopi") != "" {
		if err == nil {
			t.Error("Non png images should NOT have X-Shmoopi header at all", resp.Header.Get("X-Shmoopi"))
		} else {
			t.Error("error reading png", err)
		}
	}
}

func getImage(file string, t *testing.T) image.Image {
	newimage, err := ioutil.ReadFile(file)
	if err != nil {
		t.Fatal("Cannot read file", file, err)
	}
	img, _, err := image.Decode(bytes.NewReader(newimage))
	if err != nil {
		t.Fatal("Cannot decode image", file, err)
	}
	return img
}

func readAll(r io.Reader, t *testing.T) []byte {
	b, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal("Cannot read", err)
	}
	return b
}
func readFile(file string, t *testing.T) []byte {
	b, err := ioutil.ReadFile(file)
	if err != nil {
		t.Fatal("Cannot read", err)
	}
	return b
}
func fatalOnErr(err error,msg string, t *testing.T) {
	if err != nil {
		t.Fatal(msg,err)
	}
}
func panicOnErr(err error,msg string) {
	if err != nil {
		println(err.Error()+":-"+msg)
		os.Exit(-1)
	}
}

var _ png.FormatError
func compareImage(eImg,aImg image.Image, t *testing.T) {
	if eImg.Bounds().Dx() != aImg.Bounds().Dx() || eImg.Bounds().Dy() != aImg.Bounds().Dy() {
		t.Error("image sizes different")
		return
	}
	for i := 0; i < eImg.Bounds().Dx();i++ {
		for j := 0; j < eImg.Bounds().Dy();j++ {
			er,eg,eb,ea := eImg.At(i,j).RGBA() 
			ar,ag,ab,aa := aImg.At(i,j).RGBA() 
			if er != ar || eg != ag || eb != ab || ea != aa {
				t.Error("images different at",i,j,"vals\n",er,eg,eb,ea,"\n",ar,ag,ab,aa,aa)
				return
			}
		}
	}
}

func TestConstantImageHandler(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	var _ = client
	defer l.Close()

	//panda := getImage("panda.png", t)
	football := getImage("test_data/football.png", t)

	proxy.OnResponse().Do(HandleImage(func(img image.Image, ctx *ProxyCtx) image.Image {
		return football
	}))

	resp, err := client.Get(localFile("test_data/panda.png"))
	if err != nil {
		t.Fatal("Cannot get panda.png",err)
	}

	img,_,err := image.Decode(resp.Body)
	if err != nil {
		t.Error("decode",err)
	} else {
		compareImage(football,img,t)
	}
}

func TestImageHandler(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	var _ = client
	defer l.Close()

	football := getImage("test_data/football.png", t)

	proxy.OnResponse(UrlIs("/test_data/panda.png")).Do(HandleImage(func(img image.Image, ctx *ProxyCtx) image.Image {
		return football
	}))

	resp, err := client.Get(localFile("test_data/panda.png"))
	if err != nil {
		t.Fatal("Cannot get panda.png",err)
	}

	img,_,err := image.Decode(resp.Body)
	if err != nil {
		t.Error("decode",err)
	} else {
		compareImage(football,img,t)
	}

	// and again
	resp, err = client.Get(localFile("test_data/panda.png"))
	if err != nil {
		t.Fatal("Cannot get panda.png",err)
	}

	img,_,err = image.Decode(resp.Body)
	if err != nil {
		t.Error("decode",err)
	} else {
		compareImage(football,img,t)
	}
}

func TestChangeResp(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	var _ = client
	defer l.Close()

	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *ProxyCtx) *http.Response {
		resp.Body.Read([]byte{0})
		resp.Body = ioutil.NopCloser(new(bytes.Buffer))
		return resp
	})

	resp,err := client.Get(localFile("test_data/panda.png"))
	if err != nil {
		t.Fatal(err)
	}
	ioutil.ReadAll(resp.Body)
	_,err = client.Get(localFile("/bobo"))
	if err != nil {
		t.Fatal(err)
	}
}
func TestReplaceImage(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	defer l.Close()

	panda := getImage("test_data/panda.png", t)
	football := getImage("test_data/football.png", t)

	proxy.OnResponse(UrlIs("/test_data/panda.png")).Do(HandleImage(func(img image.Image, ctx *ProxyCtx) image.Image {
		return football
	}))
	proxy.OnResponse(UrlIs("/test_data/football.png")).Do(HandleImage(func(img image.Image, ctx *ProxyCtx) image.Image {
		return panda
	}))

	imgByPandaReq,_,err := image.Decode(bytes.NewReader(getOrFail(localFile("test_data/panda.png"),client,t)))
	fatalOnErr(err,"decode panda",t)
	compareImage(football,imgByPandaReq,t)

	imgByFootballReq,_,err := image.Decode(bytes.NewReader(getOrFail(localFile("test_data/football.png"),client,t)))
	fatalOnErr(err,"decode football",t)
	compareImage(panda,imgByFootballReq,t)
}

func getCert(c *tls.Conn, t *testing.T) []byte {
	if err := c.Handshake(); err != nil {
		t.Fatal("cannot handshake",err)
	}
	return c.ConnectionState().PeerCertificates[0].Raw
}

func TestSimpleMitm(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	var _ = l
	defer l.Close()

	proxy.MitmHost(https.Listener.Addr().String())
	//proxy.logHttps = true

	c,err := tls.Dial("tcp",https.Listener.Addr().String(),&tls.Config{InsecureSkipVerify:true})
	if err!=nil {
		t.Fatal("cannot dial to tcp server",err)
	}
	origCert := getCert(c,t)
	c.Close()

	c2,err := net.Dial("tcp",l.Listener.Addr().String())
	if err != nil {
		t.Fatal("dialing to proxy",err)
	}
	creq,err := http.NewRequest("CONNECT",https.URL,nil)
	//creq,err := http.NewRequest("CONNECT","https://google.com:443",nil)
	if err != nil {
		t.Fatal("create new request",creq)
	}
	creq.Write(c2)
	c2buf := bufio.NewReader(c2)
	resp,err := http.ReadResponse(c2buf,creq)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal("Cannot CONNECT through proxy",err)
	}
	c2tls := tls.Client(c2,&tls.Config{InsecureSkipVerify:true})
	proxyCert := getCert(c2tls,t)

	if bytes.Equal(proxyCert,origCert) {
		t.Errorf("Certificate after mitm is not different\n%v\n%v",
			base64.StdEncoding.EncodeToString(origCert),
			base64.StdEncoding.EncodeToString(proxyCert))
	}

	if resp := string(getOrFail(https.URL+"/bobo",client,t)); resp != "bobo" {
		t.Error("Wrong response when mitm",resp,"expected bobo")
	}
}

func TestMitmIsFiltered(t *testing.T) {
	client, proxy, l := oneShotProxy(t)
	var _ = l
	defer l.Close()

	//proxy.Verbose = true
	proxy.MitmHost(https.Listener.Addr().String())
	proxy.OnRequest(UrlIs("/momo")).DoFunc(func(req *http.Request, ctx *ProxyCtx) (*http.Request,*http.Response) {
		return nil,TextResponse(req,"koko")
	})

	if resp := string(getOrFail(https.URL+"/momo",client,t)); resp != "koko" {
		t.Error("Proxy should capture /momo to be koko and not",resp)
	}

	if resp := string(getOrFail(https.URL+"/bobo",client,t)); resp != "bobo" {
		t.Error("But still /bobo should be bobo and not",resp)
	}
}

func TestChunkedResponse(t *testing.T) {
	l,err := net.Listen("tcp",":10234")
	panicOnErr(err,"listen")
	defer l.Close()
	go func() {
		for i:=0; i<2; i++{
			c,err := l.Accept()
			panicOnErr(err,"accept")
			io.WriteString(c,"HTTP/1.1 200 OK\r\n"+
					"Content-Type: text/plain\r\n"+
					"Transfer-Encoding: chunked\r\n\r\n"+
					"25\r\n"+
					"This is the data in the first chunk\r\n\r\n"+
					"1C\r\n"+
					"and this is the second one\r\n\r\n"+
					"3\r\n"+
					"con\r\n"+
					"8\r\n"+
					"sequence\r\n0\r\n\r\n")
			c.Close()
		}
	}()

	c,err := net.Dial("tcp","localhost:10234")
	panicOnErr(err,"dial")
	defer c.Close()
	req,_ := http.NewRequest("GET","/",nil)
	resp,err := http.ReadResponse(bufio.NewReader(c),req)
	panicOnErr(err,"readresp")
	b,err := ioutil.ReadAll(resp.Body)
	panicOnErr(err,"readall")
	expected := "This is the data in the first chunk\r\nand this is the second one\r\nconsequence"
	if string(b) != expected {
		t.Errorf("Got `%v` expected `%v`",string(b),expected)
	}

	client, proxy, s := oneShotProxy(t)
	defer s.Close()

	proxy.OnResponse().DoFunc(func(resp *http.Response,ctx *ProxyCtx)*http.Response {
		b, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		panicOnErr(err,"readall onresp")
		if enc := resp.Header.Get("Transfer-Encoding"); enc != "" {
			t.Fatal("Chunked response should be received as plaintext",enc)
		}
		resp.Body = ioutil.NopCloser(bytes.NewBufferString(strings.Replace(string(b),"e","E",-1)))
		return resp
	})

	resp,err = client.Get("http://localhost:10234/")
	panicOnErr(err,"client.Get")
	b,err = ioutil.ReadAll(resp.Body)
	panicOnErr(err,"readall proxy")
	if string(b) != strings.Replace(expected,"e","E",-1) {
		t.Error("expected",expected,"w/ e->E. Got",string(b))
	}
}
