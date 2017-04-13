package main_test

import (
	"testing"

	. "github.com/elazarl/goproxy/examples/goproxy-cache"
	"github.com/stretchr/testify/require"
)

func TestParsingCacheControl(t *testing.T) {
	table := []struct {
		ccString string
		ccStruct CacheControl
	}{
		{`public, private="set-cookie", max-age=100`, CacheControl{
			"public":  []string{},
			"private": []string{"set-cookie"},
			"max-age": []string{"100"},
		}},
		{` foo="max-age=8, space",  public`, CacheControl{
			"public": []string{},
			"foo":    []string{"max-age=8, space"},
		}},
		{`s-maxage=86400`, CacheControl{
			"s-maxage": []string{"86400"},
		}},
		{`max-stale`, CacheControl{
			"max-stale": []string{},
		}},
		{`max-stale=60`, CacheControl{
			"max-stale": []string{"60"},
		}},
		{`" max-age=8,max-age=8 "=blah`, CacheControl{
			" max-age=8,max-age=8 ": []string{"blah"},
		}},
	}

	for _, expect := range table {
		cc, err := ParseCacheControl(expect.ccString)
		if err != nil {
			t.Fatal(err)
		}

		require.Equal(t, cc, expect.ccStruct)
		require.NotEmpty(t, cc.String())
	}
}
