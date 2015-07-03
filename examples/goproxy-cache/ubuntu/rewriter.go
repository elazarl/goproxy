package ubuntu

import (
	"log"
	"net/http"
	"net/url"
	"regexp"
)

type ubuntuRewriter struct {
	mirror *url.URL
}

var hostPattern = regexp.MustCompile(
	`https?://(security|archive).ubuntu.com/ubuntu/(.+)$`,
)

func NewRewriter() *ubuntuRewriter {
	ubuntuRewriter := &ubuntuRewriter{}

	// benchmark in the background to make sure we have the fastest
	go func() {
		mirrors, err := GetGeoMirrors()
		if err != nil {
			log.Fatal(err)
		}

		fastestMirror, err := mirrors.Fastest()
		if err != nil {
			log.Println("Error finding fastest mirror", err)
		}

		if mirrorUrl, err := url.Parse(fastestMirror); err == nil {
			log.Printf("using ubuntu mirror %s", fastestMirror)
			ubuntuRewriter.mirror = mirrorUrl
		}
	}()

	return ubuntuRewriter
}

func (ubuntuRewriter *ubuntuRewriter) Rewrite(request *http.Request) {
	url := request.URL.String()
	if ubuntuRewriter.mirror != nil && hostPattern.MatchString(url) {
		request.Header.Add("Content-Location", url)
		matches := hostPattern.FindAllStringSubmatch(url, -1)
		request.URL.Host = ubuntuRewriter.mirror.Host
		request.URL.Path = ubuntuRewriter.mirror.Path + matches[0][2]
	}
}
