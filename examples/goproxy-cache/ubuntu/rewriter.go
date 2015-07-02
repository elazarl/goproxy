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
	u := &ubuntuRewriter{}

	// benchmark in the background to make sure we have the fastest
	go func() {
		mirrors, err := GetGeoMirrors()
		if err != nil {
			log.Fatal(err)
		}

		mirror, err := mirrors.Fastest()
		if err != nil {
			log.Println("Error finding fastest mirror", err)
		}

		if mirrorUrl, err := url.Parse(mirror); err == nil {
			log.Printf("using ubuntu mirror %s", mirror)
			u.mirror = mirrorUrl
		}
	}()

	return u
}

func (ur *ubuntuRewriter) Rewrite(r *http.Request) {
	url := r.URL.String()
	if ur.mirror != nil && hostPattern.MatchString(url) {
		r.Header.Add("Content-Location", url)
		m := hostPattern.FindAllStringSubmatch(url, -1)
		r.URL.Host = ur.mirror.Host
		r.URL.Path = ur.mirror.Path + m[0][2]
	}
}
