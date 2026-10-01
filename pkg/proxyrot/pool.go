package proxyrot

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
)

// Entry is one upstream proxy. Passwords are never included in String.
type Entry struct {
	Scheme string
	Host   string
	Port   string
	User   string
	Pass   string
}

func (e Entry) String() string {
	return e.Scheme + "://" + e.HostPort()
}

func (e Entry) HostPort() string {
	return net.JoinHostPort(e.Host, e.Port)
}

func (e Entry) URL() *url.URL {
	u := &url.URL{Scheme: e.Scheme, Host: e.HostPort()}
	if e.User != "" {
		u.User = url.UserPassword(e.User, e.Pass)
	}
	return u
}

// Pool rotates through proxies loaded from a file.
type Pool struct {
	entries []Entry
	n       atomic.Uint64
}

func (p *Pool) Len() int {
	if p == nil {
		return 0
	}
	return len(p.entries)
}

// Next returns the next proxy in round-robin order.
func (p *Pool) Next() Entry {
	i := p.n.Add(1) - 1
	return p.entries[int(i%uint64(len(p.entries)))]
}

// NextURL returns the next proxy URL, or nil when the pool is empty.
func (p *Pool) NextURL() *url.URL {
	if p.Len() == 0 {
		return nil
	}
	return p.Next().URL()
}

// LoadFile reads proxies from path. Blank lines and # comments are ignored.
// Accepted forms: host:port:user:pass, host:port, and http(s)://user:pass@host:port.
func LoadFile(path string) (*Pool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		e, err := ParseLine(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s: no proxies", path)
	}
	return &Pool{entries: entries}, nil
}

// ParseLine parses one proxy record.
func ParseLine(line string) (Entry, error) {
	line = strings.TrimSpace(line)
	if strings.Contains(line, "://") {
		u, err := url.Parse(line)
		if err != nil {
			return Entry{}, err
		}
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return Entry{}, fmt.Errorf("proxy URL needs host:port")
		}
		e := Entry{Scheme: u.Scheme, Host: host, Port: port}
		if u.User != nil {
			e.User = u.User.Username()
			e.Pass, _ = u.User.Password()
		}
		if e.Scheme != "http" && e.Scheme != "https" {
			return Entry{}, fmt.Errorf("unsupported proxy scheme %q", e.Scheme)
		}
		return e, nil
	}

	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return Entry{}, fmt.Errorf("expected host:port[:user:pass]")
	}
	e := Entry{Scheme: "http", Host: parts[0], Port: parts[1]}
	if e.Host == "" || e.Port == "" {
		return Entry{}, fmt.Errorf("expected host:port[:user:pass]")
	}
	if len(parts) == 2 {
		return e, nil
	}
	if len(parts) < 4 {
		return Entry{}, fmt.Errorf("expected host:port:user:pass")
	}
	e.Pass = parts[len(parts)-1]
	e.User = strings.Join(parts[2:len(parts)-1], ":")
	if e.User == "" {
		return Entry{}, fmt.Errorf("expected host:port:user:pass")
	}
	return e, nil
}
