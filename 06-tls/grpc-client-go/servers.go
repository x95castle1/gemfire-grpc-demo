package main

import (
	"net"
	"sync"
	"time"
)

// Servers maps a peer's IP address to the GemFire server it belongs to (server1, server2), so the
// client can say which server answered each call. Docker gives a restarted container a new IP,
// so an unknown address triggers a fresh lookup (at most once a second).
type Servers struct {
	names  []string
	mu     sync.Mutex
	byIP   map[string]string
	lookup time.Time
}

func newServers(names ...string) *Servers {
	s := &Servers{names: names, byIP: map[string]string{}}
	s.refresh()
	return s
}

// name returns the server for addr, or "unknown" when it can't tell.
func (s *Servers) name(addr net.Addr) string {
	if addr == nil {
		return "none" // the call never reached a server
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.byIP[host]; ok {
		return n
	}
	if time.Since(s.lookup) > time.Second {
		s.refreshLocked()
		if n, ok := s.byIP[host]; ok {
			return n
		}
	}
	return "unknown"
}

func (s *Servers) refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
}

func (s *Servers) refreshLocked() {
	s.lookup = time.Now()
	for _, n := range s.names {
		ips, err := net.LookupHost(n)
		if err != nil {
			continue // stopped: Docker DNS drops the name until it's back
		}
		for _, ip := range ips {
			s.byIP[ip] = n
		}
	}
}
