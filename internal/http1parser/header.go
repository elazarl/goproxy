package http1parser

import "errors"

var (
	ErrBadProto    = errors.New("bad protocol")
	ErrMissingData = errors.New("missing data")
)

const (
	_eNextHeader int = iota
	_eNextHeaderN
	_eHeader
	_eHeaderValueSpace
	_eHeaderValue
	_eHeaderValueN
	_eMLHeaderStart
	_eMLHeaderValue
)

// Http1ExtractHeaders is an HTTP/1.0 and HTTP/1.1 header-only parser,
// to extract the original header names for the received request.
// Fully inspired by https://github.com/evanphx/wildcat
func Http1ExtractHeaders(input []byte) ([]string, error) {
	total := len(input)
	var path, version, headers int
	var headerNames []string

	// First line: METHOD PATH VERSION
	var methodOk bool
	for i := 0; i < total; i++ {
		switch input[i] {
		case ' ', '\t':
			methodOk = true
			path = i + 1
		}
		if methodOk {
			break
		}
	}

	if !methodOk {
		return nil, ErrMissingData
	}

	var pathOk bool
	for i := path; i < total; i++ {
		switch input[i] {
		case ' ', '\t':
			pathOk = true
			version = i + 1
		}
		if pathOk {
			break
		}
	}

	if !pathOk {
		return nil, ErrMissingData
	}

	var versionOk bool
	var readN bool
	for i := version; i < total; i++ {
		c := input[i]

		switch readN {
		case false:
			switch c {
			case '\r':
				readN = true
			case '\n':
				headers = i + 1
				versionOk = true
			}
		case true:
			if c != '\n' {
				return nil, ErrBadProto
			}
			headers = i + 1
			versionOk = true
		}
		if versionOk {
			break
		}
	}

	if !versionOk {
		return nil, ErrMissingData
	}

	// Header parsing
	state := _eNextHeader
	start := headers

	for i := headers; i < total; i++ {
		switch state {
		case _eNextHeader:
			switch input[i] {
			case '\r':
				state = _eNextHeaderN
			case '\n':
				return headerNames, nil
			case ' ', '\t':
				state = _eMLHeaderStart
			default:
				start = i
				state = _eHeader
			}
		case _eNextHeaderN:
			if input[i] != '\n' {
				return nil, ErrBadProto
			}

			return headerNames, nil
		case _eHeader:
			if input[i] == ':' {
				headerName := input[start:i]
				headerNames = append(headerNames, string(headerName))
				state = _eHeaderValueSpace
			}
		case _eHeaderValueSpace:
			switch input[i] {
			case ' ', '\t':
				continue
			}

			start = i
			state = _eHeaderValue
		case _eHeaderValue:
			switch input[i] {
			case '\r':
				state = _eHeaderValueN
			case '\n':
				state = _eNextHeader
			default:
				continue
			}
		case _eHeaderValueN:
			if input[i] != '\n' {
				return nil, ErrBadProto
			}
			state = _eNextHeader
		case _eMLHeaderStart:
			switch input[i] {
			case ' ', '\t':
				continue
			}

			start = i
			state = _eMLHeaderValue
		case _eMLHeaderValue:
			switch input[i] {
			case '\r':
				state = _eHeaderValueN
			case '\n':
				state = _eNextHeader
			default:
				continue
			}
		}
	}

	return nil, ErrMissingData
}
