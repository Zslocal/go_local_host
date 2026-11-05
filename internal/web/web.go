package web

// 内置 HTTP 服务：JSON API、Prometheus 抓取端点和一个无外部依赖的轻量仪表盘。

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Zslocal/go_local_host/internal/alert"
	"github.com/Zslocal/go_local_host/internal/monitor"
	"github.com/Zslocal/go_local_host/internal/output"
)

//go:embed dashboard.html
var dashboardHTML string

type Cache struct {
	mu         sync.RWMutex
	snap       *monitor.Snapshot
	events     []alert.Event
	started    time.Time
	staleAfter time.Duration
	maxEvents  int
}

func NewCache(staleAfter time.Duration) *Cache {
	if staleAfter <= 0 {
		staleAfter = 30 * time.Second
	}
	return &Cache{started: time.Now(), staleAfter: staleAfter, maxEvents: 50}
}

func (c *Cache) Set(s *monitor.Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap = s
}

func (c *Cache) AddEvents(evs []alert.Event) {
	if len(evs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evs...)
	if len(c.events) > c.maxEvents {
		c.events = append([]alert.Event(nil), c.events[len(c.events)-c.maxEvents:]...)
	}
}

func (c *Cache) Get() *monitor.Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap
}

func (c *Cache) Events() []alert.Event {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]alert.Event(nil), c.events...)
}

func (c *Cache) Healthy() (bool, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snap == nil {
		return false, "no sample yet"
	}
	if time.Since(c.snap.Timestamp) > c.staleAfter {
		return false, fmt.Sprintf("stale sample: %s", time.Since(c.snap.Timestamp).Truncate(time.Second))
	}
	return true, "ok"
}

type Server struct {
	cache *Cache
	token string
}

// Start 同步绑定端口（端口冲突立即报错，不会静默失败），随后在后台提供服务。
// 返回 *http.Server 供调用方优雅关闭。
func Start(addr string, cache *Cache, token string) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	s := &Server{cache: cache, token: token}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("HTTP 服务已启动: http://%s  (API /api/v1/metrics, Prometheus /metrics)", ln.Addr())
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP 服务异常: %v", err)
		}
	}()
	return srv, nil
}

// Handler 暴露路由，便于用 httptest 做端到端验证。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// 根路径只是静态 HTML，不含任何指标数据，直接放行；
	// 页面内让用户输入令牌，再由前端带 Authorization 头请求数据接口。
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/v1/metrics", s.wrap(s.handleJSON))
	mux.HandleFunc("/api/v1/alerts", s.wrap(s.handleAlerts))
	mux.HandleFunc("/metrics", s.wrap(s.handlePrometheus))
	mux.HandleFunc("/healthz", s.handleHealth)
	return mux
}

// wrap 对数据端点做 Bearer Token 校验（未配置 token 时跳过）。
// 静态页面 / 与存活探针 /healthz 不经过这里。
func (s *Server) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := r.URL.Query().Get("token")
			if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
				got = h[7:]
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="hostmon"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(dashboardHTML))
}

func (s *Server) handleJSON(w http.ResponseWriter, _ *http.Request) {
	snap := s.cache.Get()
	if snap == nil {
		http.Error(w, "no sample yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleAlerts(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(s.cache.Events())
}

func (s *Server) handlePrometheus(w http.ResponseWriter, _ *http.Request) {
	snap := s.cache.Get()
	if snap == nil {
		http.Error(w, "no sample yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_ = output.Prometheus(w, snap)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	ok, msg := s.cache.Healthy()
	if !ok {
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, "ok  started=%s  uptime=%s\n",
		s.cache.started.Format(time.RFC3339), time.Since(s.cache.started).Truncate(time.Second))
}
