package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zslocal/go_local_host/internal/alert"
	"github.com/Zslocal/go_local_host/internal/monitor"
)

func newServer(token string) (*Server, *Cache) {
	c := NewCache(time.Minute)
	c.Set(&monitor.Snapshot{
		Timestamp: time.Now(),
		Host:      monitor.HostInfo{Hostname: "node-01", Arch: "arm64"},
		CPU:       monitor.CPUInfo{Usage: 12.5},
		TCP:       monitor.TCPInfo{Established: 7},
	})
	c.AddEvents([]alert.Event{{Time: time.Now(), Level: "WARN", Message: "cpu high"}})
	return &Server{cache: c, token: token}, c
}

func TestEndpointsWithoutToken(t *testing.T) {
	s, _ := newServer("")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	cases := []struct {
		path     string
		status   int
		contains string
	}{
		{"/", 200, "hostmon"},
		{"/api/v1/metrics", 200, `"hostname":"node-01"`},
		{"/api/v1/alerts", 200, "cpu high"},
		{"/metrics", 200, "hostmon_up 1"},
		{"/healthz", 200, "ok"},
		{"/not-found", 404, ""},
	}
	for _, c := range cases {
		resp, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		body := readBody(resp)
		resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("%s: status = %d, want %d", c.path, resp.StatusCode, c.status)
		}
		if c.contains != "" && !strings.Contains(body, c.contains) {
			t.Errorf("%s: body missing %q", c.path, c.contains)
		}
	}

	// JSON 字段与磁盘/网络结构可被正常反序列化
	resp := get(t, srv.URL+"/api/v1/metrics")
	defer resp.Body.Close()
	var snap monitor.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("invalid JSON snapshot: %v", err)
	}
	if snap.CPU.Usage != 12.5 || snap.TCP.Established != 7 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestTokenAuth(t *testing.T) {
	s, _ := newServer("s3cr3t")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// 静态页面无需令牌即可打开，方便在页面内输入令牌；且页面本身不能泄露指标数据。
	index := get(t, srv.URL+"/")
	body := readBody(index)
	index.Body.Close()
	if index.StatusCode != 200 || !strings.Contains(body, "hostmon") {
		t.Fatalf("index page: status=%d, unexpected body", index.StatusCode)
	}
	// 页面里含有 JS 变量名和离线示例数据，因此只断言真实快照的 JSON 片段未出现。
	if strings.Contains(body, `"hostname":"node-01"`) || strings.Contains(body, `"usage_percent":12.5`) {
		t.Fatal("index page leaked metric data")
	}
	// 数据端点无令牌必须 401。
	for _, path := range []string{"/api/v1/metrics", "/api/v1/alerts", "/metrics"} {
		resp := get(t, srv.URL+path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without token: got %d, want 401", path, resp.StatusCode)
		}
	}
	// Bearer 头
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("Bearer auth failed: resp=%+v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
	// query 参数
	if resp, err := http.Get(srv.URL + "/metrics?token=s3cr3t"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("query auth failed: resp=%+v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
	// 健康检查不做鉴权，供探针使用
	if resp, err := http.Get(srv.URL + "/healthz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz should bypass auth: resp=%+v err=%v", resp, err)
	} else {
		resp.Body.Close()
	}
}

func TestNoSampleYet(t *testing.T) {
	c := NewCache(time.Minute)
	srv := httptest.NewServer((&Server{cache: c}).Handler())
	defer srv.Close()
	for _, path := range []string{"/api/v1/metrics", "/metrics", "/healthz"} {
		resp := get(t, srv.URL+path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s without sample: got %d, want 503", path, resp.StatusCode)
		}
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
