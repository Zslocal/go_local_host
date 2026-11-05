package monitor

// 路径映射：让同一份采集代码既能读本机 /proc，也能读容器内挂载进来的宿主 /proc。
// 设计参照 node_exporter 的 --path.procfs / --path.rootfs：
//   - 裸机：procRoot=/proc、hostRoot="" —— 与原先硬编码行为完全一致；
//   - 容器：procRoot=/host/proc、hostRoot=/host —— 由 -proc-root / -host-root 指定。
// 该文件无 build tag，因此 Windows 开发机也能编译并做单元测试。

import (
	"path"
	"strings"
)

type hostPaths struct {
	procRoot string
	hostRoot string
}

func newHostPaths(procRoot, hostRoot string) hostPaths {
	procRoot = strings.TrimRight(strings.TrimSpace(procRoot), "/")
	if procRoot == "" {
		procRoot = "/proc"
	}
	hostRoot = strings.TrimRight(strings.TrimSpace(hostRoot), "/")
	if hostRoot == "/" {
		hostRoot = "" // "/" 与空等价，避免拼出 //app 这类路径
	}
	return hostPaths{procRoot: procRoot, hostRoot: hostRoot}
}

// proc 返回 /proc 下文件的实际路径，name 是相对 /proc 的路径。
func (p hostPaths) proc(name string) string {
	return path.Join(p.procRoot, name)
}

// mount 把宿主机挂载点映射为容器内可 statfs 的路径。
// 快照中展示的仍是宿主机原始挂载点（如 /app/docker/data），映射只用于系统调用。
func (p hostPaths) mount(mountPoint string) string {
	if p.hostRoot == "" {
		return mountPoint
	}
	return path.Join(p.hostRoot, mountPoint)
}

// etc 返回 /etc 下文件的实际路径（os-release、hostname）。
func (p hostPaths) etc(name string) string {
	if p.hostRoot == "" {
		return path.Join("/etc", name)
	}
	return path.Join(p.hostRoot, "etc", name)
}

// normalize 把挂载表里的路径拆成 (容器内可 statfs 的路径, 展示用的宿主机路径)。
// /proc/mounts 实际是 self/mounts 的软链，反映的是**读取进程的 mount namespace**：
//   - 容器内挂载表常已带 /host 前缀（如 /host/app/docker/data）→ 直接 statfs，展示时剥掉前缀；
//   - 宿主机原始路径（如 /app/docker/data）→ 补 /host 前缀后 statfs，展示保持原样。
//
// 两种形态都要支持，否则会拼出 /host/host/... 让磁盘列表整体为空。
func (p hostPaths) normalize(mountPoint string) (statPath, display string) {
	if p.hostRoot == "" {
		return mountPoint, mountPoint
	}
	if mountPoint == p.hostRoot {
		return p.hostRoot, "/"
	}
	prefix := p.hostRoot + "/"
	if strings.HasPrefix(mountPoint, prefix) {
		return mountPoint, "/" + strings.TrimPrefix(mountPoint, prefix)
	}
	return p.mount(mountPoint), mountPoint
}
