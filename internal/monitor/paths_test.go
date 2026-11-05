package monitor

import "testing"

func TestHostPathsBareMetal(t *testing.T) {
	p := newHostPaths("/proc", "")
	for _, tc := range []struct{ got, want string }{
		{p.proc("stat"), "/proc/stat"},
		{p.proc("net/dev"), "/proc/net/dev"},
		{p.etc("os-release"), "/etc/os-release"},
		{p.mount("/app/docker/data"), "/app/docker/data"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestHostPathsContainer(t *testing.T) {
	p := newHostPaths("/host/proc/", "/host/")
	for _, tc := range []struct{ got, want string }{
		{p.proc("stat"), "/host/proc/stat"},
		{p.proc("net/tcp6"), "/host/proc/net/tcp6"},
		{p.proc("sys/kernel/osrelease"), "/host/proc/sys/kernel/osrelease"},
		{p.etc("os-release"), "/host/etc/os-release"},
		{p.etc("hostname"), "/host/etc/hostname"},
		{p.mount("/app/docker/data"), "/host/app/docker/data"},
		{p.mount("/"), "/host"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestHostPathsNormalizeMountPoints(t *testing.T) {
	bare := newHostPaths("/proc", "")
	if sp, d := bare.normalize("/app/docker/data"); sp != "/app/docker/data" || d != "/app/docker/data" {
		t.Fatalf("bare metal: stat=%q display=%q", sp, d)
	}
	ctr := newHostPaths("/host/proc", "/host")
	for _, tc := range []struct{ in, stat, display string }{
		{"/host/app/docker/data", "/host/app/docker/data", "/app/docker/data"},
		{"/app/docker/data", "/host/app/docker/data", "/app/docker/data"},
		{"/host", "/host", "/"},
		{"/", "/host", "/"},
	} {
		sp, d := ctr.normalize(tc.in)
		if sp != tc.stat || d != tc.display {
			t.Errorf("normalize(%q) = (%q,%q), want (%q,%q)", tc.in, sp, d, tc.stat, tc.display)
		}
	}
}

func TestHostPathsNormalization(t *testing.T) {
	// 空 procRoot 兜底为 /proc；hostRoot="/" 与空等价，不能拼出 //app。
	for _, tc := range []struct{ proc, host, wantProc, wantMount string }{
		{"", "", "/proc/stat", "/app"},
		{"/", "/", "/proc/stat", "/app"},
		{"  /host/proc  ", " /host ", "/host/proc/stat", "/host/app"},
	} {
		p := newHostPaths(tc.proc, tc.host)
		if got := p.proc("stat"); got != tc.wantProc {
			t.Errorf("newHostPaths(%q,%q).proc(stat) = %q, want %q", tc.proc, tc.host, got, tc.wantProc)
		}
		if got := p.mount("/app"); got != tc.wantMount {
			t.Errorf("newHostPaths(%q,%q).mount(/app) = %q, want %q", tc.proc, tc.host, got, tc.wantMount)
		}
	}
}
