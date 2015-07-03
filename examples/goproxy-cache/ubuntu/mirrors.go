package ubuntu

import (
	"bufio"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

const (
	mirrorsUrl       = "http://mirrors.ubuntu.com/mirrors.txt"
	benchmarkUrl     = "dists/trusty/Release"
	benchmarkTimes   = 3
	benchmarkBytes   = 1024 * 512 // 512Kb
	benchmarkTimeout = 20         // 20 seconds
	mirrorTimeout = 15 //seconds
)

type Mirrors struct {
	URLs []string
}

func GetGeoMirrors() (mirrors Mirrors, err error) {
	response, err := http.Get(mirrorsUrl)
	if err != nil {
		return
	}

	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	mirrors.URLs = []string{}

	// read urls line by line
	for scanner.Scan() {
		mirrors.URLs = append(mirrors.URLs, scanner.Text())
	}

	return mirrors, scanner.Err()
}

func (mirrors Mirrors) Fastest() (string, error) {
	benchMarkResultChannel := make(chan benchmarkResult)

	// kick off all benchmarks in parallel
	log.Printf("Start benchmarking mirrors")
	for _, url := range mirrors.URLs {
		go func(u string) {
			duration, err := mirrors.benchmark(u, benchmarkTimes)
			if err == nil {
				benchMarkResultChannel <- benchmarkResult{u, duration}
			}
		}(url)
	}
	
	totalMirrors := len(mirrors.URLs)
	if 3 < totalMirrors {
		totalMirrors = 3
	}

	// wait for the fastest results to come back
	results, err := mirrors.readResults(benchMarkResultChannel, totalMirrors)
	log.Printf("Finished benchmarking mirrors")
	if len(results) == 0 {		
		return "", errors.New("No results found: " + err.Error())
	} 

	return results[0].URL, nil
}

func (mirrors Mirrors) readResults(benchmarkResultChannel <-chan benchmarkResult, size int) (benchmarkResults []benchmarkResult, err error) {
	for {
		select {
		case r := <-benchmarkResultChannel:
			benchmarkResults = append(benchmarkResults, r)
			
			if len(benchmarkResults) >= size {
				return benchmarkResults, nil
			}
		case <-time.After(benchmarkTimeout * time.Second):
			return benchmarkResults, errors.New("Timed out waiting for results")
		}
	}
}

func (mirrors Mirrors) benchmark(url string, times int) (time.Duration, error) {
	var totalTimeTaken int64
	var duration time.Duration
	url = url + benchmarkUrl
	
	timeout := time.Duration(mirrorTimeout * time.Second)
	client := http.Client{
	    Timeout: timeout,
	}

	for i := 0; i < times; i++ {
		timer := time.Now()
		
		response, err := client.Get(url)
		if err != nil {
			return duration, err
		}
		
		defer response.Body.Close()
        _, err = ioutil.ReadAll(response.Body)
		
		if err != nil {
			return duration, err
		}

		totalTimeTaken = totalTimeTaken + int64(time.Since(timer))
	}

	return time.Duration(totalTimeTaken / int64(times)), nil
}

type benchmarkResult struct {
	URL      string
	Duration time.Duration
}
