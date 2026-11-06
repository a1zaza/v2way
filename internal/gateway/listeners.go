package gateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

type listenerContextKey struct{}
type listenerIdentity struct {
	Port     int
	Protocol string
}
type ListenerInfo struct {
	Protocol string   `json:"protocol"`
	Address  string   `json:"address"`
	Port     int      `json:"port"`
	Sites    []string `json:"sites"`
}
type binding struct {
	identity listenerIdentity
	listener net.Listener
	server   *http.Server
}

// Listeners serializes reservation and activation with publication. Newly reserved
// sockets do not serve requests until the durable configuration is committed.
type Listeners struct {
	mu       sync.Mutex
	engine   *Engine
	host     string
	active   map[int]*binding
	retiring map[*binding]bool
	closed   bool
	failures chan error
}
type ListenerPlan struct {
	owner   *Listeners
	desired map[int]listenerIdentity
	added   map[int]*binding
	done    bool
}

func NewListeners(e *Engine, host string) *Listeners {
	return &Listeners{engine: e, host: host, active: map[int]*binding{}, retiring: map[*binding]bool{}, failures: make(chan error, 1)}
}
func (m *Listeners) Errors() <-chan error { return m.failures }
func (m *Listeners) Prepare(s *Snapshot) (*ListenerPlan, error) {
	m.mu.Lock()
	p := &ListenerPlan{owner: m, desired: map[int]listenerIdentity{}, added: map[int]*binding{}}
	fail := func(err error) (*ListenerPlan, error) { p.Abort(); return nil, err }
	if m.closed {
		return fail(errors.New("业务监听器已关闭"))
	}
	for _, site := range s.sites {
		if !site.Enabled {
			continue
		}
		if site.HTTPPort > 0 {
			p.desired[site.HTTPPort] = listenerIdentity{site.HTTPPort, "http"}
		}
		if site.HTTPS {
			p.desired[site.HTTPSPort] = listenerIdentity{site.HTTPSPort, "https"}
		}
	}
	for port, id := range p.desired {
		if old := m.active[port]; old != nil {
			if old.identity.Protocol != id.Protocol {
				return fail(fmt.Errorf("端口 %d 正在使用 %s，请先发布移除该端口，再更换协议", port, old.identity.Protocol))
			}
			continue
		}
		addr := net.JoinHostPort(m.host, strconv.Itoa(port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return fail(fmt.Errorf("无法监听端口 %d（占用或权限不足）：%w", port, err))
		}
		b := &binding{identity: id, listener: listener}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.engine.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), listenerContextKey{}, id)))
		})
		b.server = &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10, ErrorLog: log.New(os.Stderr, "业务监听: ", log.LstdFlags)}
		if id.Protocol == "https" {
			b.server.TLSConfig = m.engine.TLSConfig(port)
		}
		p.added[port] = b
	}
	return p, nil
}
func (p *ListenerPlan) Abort() {
	if p == nil || p.done {
		return
	}
	p.done = true
	for _, b := range p.added {
		b.listener.Close()
	}
	p.owner.mu.Unlock()
}
func (p *ListenerPlan) Activate(s *Snapshot) {
	if p == nil || p.done {
		panic("业务监听计划不可重复使用")
	}
	m := p.owner
	m.engine.Activate(s)
	for port, b := range m.active {
		if _, ok := p.desired[port]; !ok {
			b.listener.Close() // stop accepting, preserve existing requests and upgraded connections
			delete(m.active, port)
			m.retiring[b] = true
			go func() {
				// Closing a listener must not impose a new lifetime on existing SSE
				// requests. Process shutdown separately enforces its bounded deadline.
				_ = b.server.Shutdown(context.Background())
				m.mu.Lock()
				delete(m.retiring, b)
				m.mu.Unlock()
			}()
		}
	}
	for port, b := range p.added {
		m.active[port] = b
		go func() {
			var err error
			if b.identity.Protocol == "https" {
				err = b.server.ServeTLS(b.listener, "", "")
			} else {
				err = b.server.Serve(b.listener)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				select {
				case m.failures <- fmt.Errorf("业务端口 %d 监听异常：%w", b.identity.Port, err):
				default:
				}
			}
		}()
	}
	p.done = true
	m.mu.Unlock()
}
func (m *Listeners) Info() []ListenerInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ListenerInfo{}
	for port, b := range m.active {
		names := []string{}
		for _, s := range m.engine.Current().sites {
			if s.Enabled && (b.identity.Protocol == "http" && s.HTTPPort == port || b.identity.Protocol == "https" && s.HTTPS && s.HTTPSPort == port) {
				names = append(names, s.Name)
			}
		}
		out = append(out, ListenerInfo{b.identity.Protocol, b.listener.Addr().String(), port, names})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
func (m *Listeners) Close() {
	m.mu.Lock()
	m.closed = true
	all := []*binding{}
	for _, b := range m.active {
		all = append(all, b)
		b.listener.Close()
	}
	for b := range m.retiring {
		all = append(all, b)
	}
	m.active = map[int]*binding{}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, b := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.server.Shutdown(ctx); err != nil {
				b.server.Close()
			}
		}()
	}
	wg.Wait()
}
