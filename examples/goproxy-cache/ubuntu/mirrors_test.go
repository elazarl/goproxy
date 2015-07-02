package ubuntu

import (
	"log"
	"testing"
)

func TestMirrors(t *testing.T) {
	mirrors, err := GetGeoMirrors()
	if err != nil {
		t.Fatal(err)
	}

	if len(mirrors.URLs) == 0 {
		t.Fatal("No mirrors found")
	}
}

func TestMirrorsBenchmark(t *testing.T) {
	mirrors, err := GetGeoMirrors()
	if err != nil {
		t.Fatal(err)
	}

	fastest, err := mirrors.Fastest()
	if err != nil {
		t.Fatal(err)
	}

	log.Printf("Fastest mirror is %s", fastest)
}
