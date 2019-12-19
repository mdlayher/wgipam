package wgdynamic

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// A kvParser parses streams of key=value pairs.
type kvParser struct {
	s    *bufio.Scanner
	err  error
	werr Error
	k, v string
}

// newKVParser creates a kvParser that reads from r.
func newKVParser(r io.Reader) *kvParser {
	return &kvParser{
		s: bufio.NewScanner(r),
	}
}

// Next advances to the next key=value pair if possible.
func (p *kvParser) Next() bool {
	if p.err != nil || !p.s.Scan() || p.s.Text() == "" {
		// Hit an error, no more input, or we've reached the end of input.
		return false
	}

	kvs := strings.Split(p.s.Text(), "=")
	if len(kvs) != 2 {
		p.err = fmt.Errorf("wgdynamic: malformed key/value pair in response: %q", p.s.Text())
		return false
	}

	// Set up internal state for calling other functions.
	p.k, p.v = kvs[0], kvs[1]

	// Handle any errors internally and recursively call Next so that the caller
	// does not observe any error key/value pairs.
	switch p.k {
	case "errno":
		p.werr.Number = p.Int()
		return p.Next()
	case "errmsg":
		p.werr.Message = p.String()
		return p.Next()
	}

	return true
}

// Key returns the current key of a key/value pair.
func (p *kvParser) Key() string { return p.k }

// Int parses the current value as an integer.
func (p *kvParser) Int() int {
	if p.err != nil {
		return 0
	}

	v, err := strconv.Atoi(p.v)
	if err != nil {
		p.err = err
		return 0
	}

	return v
}

// String returns the current value.
func (p *kvParser) String() string {
	if p.err != nil {
		return ""
	}

	return p.v
}

// IPNet parses the current value as a *net.IPNet.
func (p *kvParser) IPNet() *net.IPNet {
	if p.err != nil {
		return nil
	}

	ip, ipn, err := net.ParseCIDR(p.v)
	if err != nil {
		p.err = err
		return nil
	}

	// We want to return the actual allocated IP address along with its proper
	// subnet mask, so replace the first network address with the actual IP
	// address.
	ipn.IP = ip

	return ipn
}

// Err returns any errors encountered during parsing.
func (p *kvParser) Err() error {
	// First, errors from the underlying scanner.
	if err := p.s.Err(); err != nil {
		return err
	}

	// Next, errors encountered while parsing a value.
	if p.err != nil {
		return p.err
	}

	// Finally, any protocol errors which may have been encountered.
	if p.werr.Number != 0 {
		return &p.werr
	}

	return nil
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
