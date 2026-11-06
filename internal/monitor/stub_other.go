//go:build !linux

package monitor

import "errors"

// ErrUnsupported 表示当前平台没有 /proc，无法采集 Linux 主机指标。
// 保留该文件是为了能在 Windows/macOS 上交叉编译和运行单元测试。
var ErrUnsupported = errors.New("hostmon: metrics collection requires Linux (/proc)")

func collectRaw(Config) (*rawSnapshot, error) {
	return nil, ErrUnsupported
}

// enrichProcDetails 在非 Linux 平台无事可做（没有 /proc/<pid>/cmdline）。
func enrichProcDetails(*Snapshot, hostPaths) {}
