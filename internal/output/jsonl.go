package output

import (
	"encoding/json"
	"io"
	"os"
	"sync"

	"github.com/Zslocal/go_local_host/internal/monitor"
)

// JSONL 以「一行一个完整快照」的方式追加写入，便于 tail -f、jq 或日志采集器消费。
type JSONL struct {
	mu sync.Mutex
	f  *os.File
}

func OpenJSONL(path string) (*JSONL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &JSONL{f: f}, nil
}

func (j *JSONL) Write(s *monitor.Snapshot) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	// os.File 无用户态缓冲，写完后立刻对其他进程可见。
	_, err = j.f.Write(b)
	return err
}

func (j *JSONL) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.f.Close()
}

// JSON 输出单个快照（紧凑单行，方便管道处理；-pretty 时用缩进格式）。
func JSON(w io.Writer, s *monitor.Snapshot, pretty bool) error {
	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(s)
}
