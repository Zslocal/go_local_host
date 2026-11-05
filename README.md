# hostmon · Linux 服务器监控采集器

用 Go 写的单文件、零第三方依赖监控程序，采集 **CPU、内存、Swap、磁盘容量与 IO、网络流量、TCP 连接、系统负载与运行时间**。所有数据直接读 `/proc` 与 `statfs(2)`，不调用 `top`/`free`/`df`/`iostat` 等外部命令，也不需要 root 权限。

编译产物是纯静态 ELF（`CGO_ENABLED=0`），`scp` 上去就能跑，对 aarch64 / Anolis OS 这类环境没有 glibc 版本兼容问题。

```
go.mod                          # 仅 module 声明，无任何 require
cmd/hostmon/main.go             # CLI 入口：参数、主循环、信号优雅退出
internal/monitor/
  types.go                      # 快照与指标结构（JSON 字段定义）
  parse.go                      # /proc 文本纯解析（可跨平台单测）
  collect_linux.go              # Linux 采集实现（//go:build linux）
  paths.go                      # /proc 与 rootfs 路径映射（容器部署用，可跨平台单测）
  stub_other.go                 # 非 Linux 桩，保证 Windows 可编译/单测
  sampler.go                    # 两次采样差分引擎（CPU%、IOPS、速率）
internal/output/                 # 终端文本 / JSONL / Prometheus 三种输出
internal/alert/                   # 带滞回与去重的阈值告警
internal/web/                     # HTTP API + Prometheus 端点 + 内嵌仪表盘
build.sh                          # 交叉编译 amd64 / arm64
Dockerfile                        # scratch 静态镜像（只有一个 ELF）
docker-compose.yml                # 最小权限、宿主机视角的容器部署
.dockerignore
```

## 编译

源码仓库：<https://github.com/Zslocal/go_local_host>，版本标记 `v0.1.0`。module path 已与仓库一致，克隆后即可编译：

```bash
git clone https://github.com/Zslocal/go_local_host.git
cd go_local_host && bash build.sh      # 产出 dist/hostmon-linux-{amd64,arm64}
```

开发机是 Windows 也能直接交叉编译（Git Bash）：

```bash
bash build.sh                 # 产出 dist/hostmon-linux-amd64 与 dist/hostmon-linux-arm64
```

等价的手动命令 / PowerShell：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w -X main.version=0.1.0" -o hostmon ./cmd/hostmon
```

```powershell
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="arm64"
go build -trimpath -ldflags "-s -w -X main.version=0.1.0" -o hostmon ./cmd/hostmon
```

部署：

```bash
scp dist/hostmon-linux-arm64 user@server:/usr/local/bin/hostmon
ssh user@server 'chmod +x /usr/local/bin/hostmon && hostmon -once'
```

> 采集依赖 `/proc`，程序在非 Linux 上会明确拒绝运行；Windows 上仍可 `go test ./...` 验证全部解析与差分逻辑。

## 快速开始

```bash
hostmon -once                                  # 采集一次，打印完整文本报告
hostmon -interval 5s -cores -all-lines        # 持续刷新，显示每核与全部设备
hostmon -once -format json | jq .cpu          # 脚本 / 定时任务集成
hostmon -format json -log /var/log/hostmon.jsonl -interval 10s   # JSONL 落盘，供日志采集器消费

# 常驻：仪表盘 + Prometheus 抓取端点 + 告警 + 落盘（推荐）
hostmon -web 127.0.0.1:9100 -web-token '换成长随机串' \
        -log /var/log/hostmon.jsonl -quiet
```

文本输出示例：

```
== 2026-11-05 14:36:59  prod-node-01  Anolis OS 8.9/arm64  8 CPU  Kunpeng-920
   kernel 5.10.134 | uptime 42d 3h 12m (42d03h12m) | boot 2026-09-24 11:24:00 | window 2.00s
   load 0.82 / 1.05 / 0.98 (per-core 0.10) | procs 3 running / 412 total

CPU     8.42%  usr 5.10 sys 2.01 iowait 0.80 irq 0.10 softirq 0.30 steal 0.00 nice 0.11 idle 91.58
Memory 39.49%  12.0G used / 30.5G total | avail 18.0G | free 2.0G | buf/cache 4.4G
Swap   50.00%  1.0G used / 2.0G total

MOUNT                 FS     TOTAL   USED    AVAIL   USE%  INODE%
  /app/docker/data    xfs    500.0G  410.2G  89.8G   82.04  1.12
  /                   ext4   100.0G  62.3G   37.7G   62.30  4.20

DISK      r/s    w/s    READ/s   WRITE/s  IOPS  UTIL%  LAT(ms)
  nvme0n1  12.5   30.0   1.2M/s   4.8M/s   42.5  8.20  0.42

NET       RX/s     TX/s     RXpkt/s  TXpkt/s  RX ERR/DROP (total)
  eth0     1.2M/s   800.0K/s 1200     900      0 / 0

TCP       estab 128 | listen 24 | time_wait 42 | total 200
```

## 参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-interval` | `2s` | 采样间隔，最小 100ms；同时决定速率类指标的差分窗口 |
| `-warmup` | `700ms` | **首次**采样的内部差分窗口，保证 `-once` 也能得到真实 CPU%/速率 |
| `-proc-root` | `/proc` | 宿主机 `/proc` 的挂载路径；容器内用 `/host/proc`，也可用 `HOSTMON_PROC_ROOT` |
| `-host-root` | 空 | 宿主机根文件系统的挂载路径；容器内用 `/host`，用于 `statfs` 与 `/etc/os-release`，也可用 `HOSTMON_HOST_ROOT` |
| `-once` | off | 采集一次即退出 |
| `-format` | `text` | `text` / `json` / `prom`；`json` 为单行紧凑格式，适合管道 |
| `-pretty` | off | `-format json` 时缩进输出 |
| `-cores` | off | 显示每个逻辑核的使用率 |
| `-all-lines` | off | 文本输出不限制挂载点/磁盘/网卡行数（默认 8/6/6） |
| `-top` | off | 持续模式 ANSI 清屏重绘，类似 `top` |
| `-net-all` | off | 把 `lo`/`veth*`/`cni*`/`br-*` 等虚拟网卡也纳入统计 |
| `-web` | 空 | 启用 HTTP，如 `127.0.0.1:9100`；提供仪表盘、JSON API、`/metrics` |
| `-web-token` | 空 | HTTP Bearer Token，也可 `?token=`；为空则无鉴权。容器里建议用 `HOSTMON_WEB_TOKEN` 环境变量，避免出现在 `docker inspect` 里 |
| `-log` | 空 | 每次快照追加一行 JSON 到指定文件 |
| `-quiet` | off | 启用 `-web` 时不在终端打印快照 |
| `-cpu` / `-mem` / `-disk` | `85` / `90` / `90` | 告警阈值（%），`0` 关闭；`-disk` 同时管容量与 inode |
| `-disk-util` | `95` | 块设备 busy 告警阈值（%） |
| `-load` | `0` | `load1 / 核数` 的倍数阈值，如 `2`，`0` 关闭 |
| `-net-drop` | off | 网卡出现新增错包/丢包时告警 |
| `-alert-repeat` | `5m` | 持续告警的重复间隔 |
| `-no-alert` | off | 关闭告警 |
| `-version` | — | 显示版本 |

告警写在 **stderr**，不会污染 `-format json` 的 stdout 管道。带 3 个百分点滞回（避免阈值边缘抖动）、状态变化才提示、持续告警按间隔重复，并输出恢复事件：

```
[WARN] 14:42:03  CPU usage 91.20% >= threshold 85.00%
[WARN] 14:47:03  CPU usage still high: 90.10% >= 85.00%
[ OK ] 14:49:03  CPU usage recovered: 72.10% (threshold 85.00%)
```

## HTTP 接口

| 路径 | 说明 |
|---|---|
| `/` | 内嵌仪表盘（纯原生 JS/CSS，无 CDN，离线可用，默认 2s 自动刷新）。**静态页面无需令牌**，启用 `-web-token` 时在页面右上角输入即可，令牌存 sessionStorage 并用 `Authorization` 头发送 |
| `/api/v1/metrics` | 最新快照 JSON，字段与 `-format json` 完全一致 |
| `/api/v1/alerts` | 最近 50 条告警/恢复事件 |
| `/metrics` | Prometheus text exposition format |
| `/healthz` | 存活探针；快照过期（3 个周期未更新）返回 503 |

安全建议（默认不监听公网）：

```nginx
# 只绑定回环 + Nginx 反代 + 认证/白名单
server {
    listen 443 ssl;
    server_name mon.example.com;
    location / {
        allow 10.0.0.0/8;  deny all;
        proxy_pass http://127.0.0.1:9100;
        proxy_set_header Host $host;
    }
    # ssl_certificate ...
}
```

若必须直接暴露端口，务必设置 `-web-token`（**不要留 `CHANGE_ME` 这种占位符**）。浏览器直接打开 `http://ip:9100/` 会看到仪表盘和令牌输入框；也兼容 `?token=xxx`，但令牌会留在浏览器历史与访问日志里，优先用页面输入框。Prometheus 抓取端用 `Authorization: Bearer <token>`。

鉴权边界：`/` 是纯静态 HTML、不含任何主机数据，所以放行；`/api/v1/*` 和 `/metrics` 严格校验令牌；`/healthz` 不做鉴权（供探针使用），不要把它暴露到公网。

仪表盘响应式：顶部 4 张 KPI 卡片固定只有两种排布 —— 视口 ≥901px 为 **1×4**，≤900px 为 **2×2**，不会出现 3+1 的孤行；主内容在 1180px 以下改单栏，侧栏为「TCP/进程 + 主机信息」等高并排、「告警」横跨整宽，因此不会出现孤行空白；760px 以下全部单栏、表格可横向滚动并显示「横滑」提示，390px 手机宽度同样可用。信息表（`.kv`）标签列最小 84px、最多两行，长值自动换行，避免中文标签被挤成竖排。

## Prometheus / Grafana

```yaml
scrape_configs:
  - job_name: hostmon
    scrape_interval: 15s     # 大于 -interval 即可
    static_configs:
      - targets: ['127.0.0.1:9100']
    # 如启用 token：
    # authorization: { type: Bearer, credentials: '你的token' }
```

主要指标（全部带 `hostmon_` 前缀，与 node_exporter 共存不冲突）：

| 指标 | 类型 | 标签 |
|---|---|---|
| `hostmon_up`, `hostmon_info`, `hostmon_cpu_count` | gauge | hostname/os/kernel/arch/cpu_model |
| `hostmon_uptime_seconds`, `hostmon_boot_time_seconds` | gauge | — |
| `hostmon_load1/5/15`, `hostmon_load1_per_cpu` | gauge | — |
| `hostmon_cpu_usage_percent`, `hostmon_cpu_percent`, `hostmon_cpu_core_usage_percent` | gauge | mode / core |
| `hostmon_memory_bytes`, `hostmon_memory_usage_percent` | gauge | kind |
| `hostmon_swap_bytes`, `hostmon_swap_usage_percent` | gauge | kind |
| `hostmon_filesystem_bytes`, `hostmon_filesystem_usage_percent`, `hostmon_filesystem_inodes_usage_percent` | gauge | device/mountpoint/fstype/kind |
| `hostmon_disk_{reads,writes}_per_second`, `hostmon_disk_{read,write}_bytes_per_second`, `hostmon_disk_io_in_flight_percent`, `hostmon_disk_avg_latency_ms` | gauge | device |
| `hostmon_disk_{reads,writes,read_bytes,write_bytes}_total` | counter | device |
| `hostmon_network_{receive,transmit}_bytes_per_second`, `..._packets_per_second` | gauge | interface |
| `hostmon_network_{receive,transmit}_{bytes,errors,drops}_total` | counter | interface |
| `hostmon_tcp_connections`, `hostmon_processes`, `hostmon_processes_created_total` | gauge/counter | state |

> `/metrics` 返回的是**最近一次采集的缓存值**，不会因 Prometheus 抓取而额外触发采样，抓取频率可以高于 `-interval`。

## systemd 常驻

`/etc/systemd/system/hostmon.service`：

```ini
[Unit]
Description=hostmon server metrics
After=network.target

[Service]
Type=simple
EnvironmentFile=-/etc/hostmon.env
ExecStart=/usr/local/bin/hostmon \
  -interval 5s -web 127.0.0.1:9100 -web-token ${HOSTMON_WEB_TOKEN} \
  -log /var/log/hostmon.jsonl -quiet
Restart=always
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log
# 只读 /proc，无需 root
User=hostmon
Group=hostmon

[Install]
WantedBy=multi-user.target
```

```bash
useradd -r -s /sbin/nologin hostmon
touch /var/log/hostmon.jsonl && chown hostmon: /var/log/hostmon.jsonl
# 令牌写进独立文件，不要留在 unit 或命令行历史里
printf 'HOSTMON_WEB_TOKEN=%s\n' "$(openssl rand -hex 24)" > /etc/hostmon.env
chmod 600 /etc/hostmon.env
systemctl daemon-reload && systemctl enable --now hostmon
journalctl -u hostmon -f
```

日志轮转 `/etc/logrotate.d/hostmon`（JSONL 追加写，可直接 copytruncate）：

```
/var/log/hostmon.jsonl {
    daily
    rotate 14
    compress
    missingok
    notifempty
    copytruncate
}
```

## Docker 部署

**可以跑，而且很适合**：程序是 `CGO_ENABLED=0` 的静态单文件，镜像 `FROM scratch` 即可，里面只有一个 ELF，没有 shell、libc 或包管理器。

但**默认容器视角是失真的**，直接 `docker run` 会拿到混合数据：

| 指标 | 裸容器（不挂载） | 按下面配置后 |
|---|---|---|
| CPU / 内存 / 负载 / 运行时间 | 宿主机全局值（基本准确） | 宿主机全局值 |
| 磁盘容量 / inode | 只有容器 rootfs 与已挂载卷 | 宿主机根分区 + 传播/显式挂载进来的目录（见下方「磁盘容量的边界」） |
| 磁盘 IO | `/proc/diskstats` 是全局的 | 宿主机全部块设备 |
| 网卡速率 / TCP 连接 | `/proc/net/*` 属于**容器 netns**，只有 eth0 | 宿主机全部网卡与连接 |
| 主机名 / 发行版 | 容器 ID / 镜像发行版 | 宿主机真实值 |

要拿到尽量完整的宿主机视角，需要两件事：**把宿主机 `/proc` 与根只读挂进来**，再用 **`-proc-root` / `-host-root` 告诉程序去哪读**（设计同 node_exporter 的 `--path.procfs` / `--path.rootfs`）。其中磁盘容量受 mount namespace 限制，见下方「磁盘容量的边界」。

### 挂载与权限清单

| 挂载 / 选项 | 必需 | 用途 |
|---|---|---|
| `-v /proc:/host/proc:ro` | ✅ | `stat` / `meminfo` / `loadavg` / `uptime` / `mounts` / `diskstats` / `net/dev` / `net/tcp` / `cpuinfo`。**只读，不要加 `:z` / `:Z`**（会重标记宿主机 `/proc`） |
| `-v /:/host:ro,rslave` | 推荐 | 让 `statfs(2)` 能访问宿主机根分区；同时读 `/host/etc/os-release`、`/host/etc/hostname`。`rslave` 让宿主机后续挂载/卸载事件传播进容器。只读暴露整个根，配合下面的最小权限使用 |
| `--network=host` | 推荐 | 让 `/host/proc/net/*` 解析到宿主机 netns；不加则只能看到容器网卡。与 `ports` 互斥 |
| `-v /usr/share/zoneinfo:/usr/share/zoneinfo:ro` + `TZ` | 可选 | `scratch` 镜像不带 tzdata，挂上后时间显示与宿主机一致 |
| `--pid=host` | ❌ 不需要 | 不读 `/proc/<pid>/`，共享 PID namespace 无收益 |
| `/sys` | ❌ 不需要 | 磁盘 IO 走 `/proc/diskstats`，不读 sysfs |
| `--privileged` / `--cap-add` / root | ❌ 不需要 | 只是读文件 + `statfs(2)`，`--cap-drop=ALL` 就能跑 |

推荐的最小权限组合：

```
--user 65532:65532 --read-only --cap-drop=ALL --security-opt no-new-privileges
```

> **SELinux**：Anolis OS / CentOS 等 enforcing 环境下，若读 `/host` 下文件报 `permission denied`，可在评估风险后加 `--security-opt label=disable`；**不要**对 `/proc` 做 `:z`/`:Z` 重标记。

### docker run

```bash
# 1) 构建镜像（aarch64 用默认；x86_64 加 --build-arg HOSTMON_BIN=dist/hostmon-linux-amd64）
bash build.sh
docker build -t hostmon:local .

# 2) 运行
docker run -d --name hostmon --restart unless-stopped \
  --network host \
  --user 65532:65532 --read-only --cap-drop ALL \
  --security-opt no-new-privileges \
  -v /proc:/host/proc:ro \
  -v /:/host:ro,rslave \
  -v /usr/share/zoneinfo:/usr/share/zoneinfo:ro \
  -e TZ=Asia/Shanghai \
  -e HOSTMON_WEB_TOKEN='换成强随机串' \
  hostmon:local \
    -interval 5s -web 127.0.0.1:9100 \
    -proc-root /host/proc -host-root /host -quiet

# 3) 验证（host network 下 127.0.0.1:9100 就是宿主机回环）
curl -H 'Authorization: Bearer 换成强随机串' http://127.0.0.1:9100/api/v1/metrics | jq '.host, .mounts'
docker exec hostmon /hostmon -once -proc-root /host/proc -host-root /host
```

### docker-compose（推荐）

仓库里的 `docker-compose.yml` 已包含上述全部配置：

```bash
bash build.sh
echo 'HOSTMON_WEB_TOKEN=换成强随机串' > .env        # .env 已加入 .gitignore
# x86_64 服务器再执行：echo 'HOSTMON_BIN=dist/hostmon-linux-amd64' >> .env
docker compose up -d --build
docker compose logs -f
```

compose 用 `network_mode: host`，所以 `-web 127.0.0.1:9100` 就是宿主机回环，**不需要 `ports`、不会把端口暴露到公网**。若改用 bridge 网络，容器内必须绑 `0.0.0.0:9100`，宿主侧用 `ports: ["127.0.0.1:9100:9100"]` 限制 —— 两种模式别混用。

令牌走 `HOSTMON_WEB_TOKEN` 环境变量而非 `command`，避免出现在 `docker inspect` 里。镜像内没有 curl/wget，`/healthz` 由宿主机或 Prometheus 侧探针检查。

### 三个容器专用参数

| 参数 | 环境变量 | 容器内值 | 作用 |
|---|---|---|---|
| `-proc-root` | `HOSTMON_PROC_ROOT` | `/host/proc` | 所有 `/proc/*` 读取的根 |
| `-host-root` | `HOSTMON_HOST_ROOT` | `/host` | 挂载点 `statfs` 前缀 + `/etc/os-release`、`/etc/hostname` |
| `-web-token` | `HOSTMON_WEB_TOKEN` | 强随机串 | HTTP 鉴权；命令行显式传入优先于环境变量 |

不设置时 `-proc-root=/proc`、`-host-root` 为空，行为与裸机完全一致，**已有的 systemd / 直接运行部署无需改动**。快照里展示的挂载点仍是宿主机原始路径（如 `/app/docker/data`），`/host` 前缀只用于 `statfs(2)` 系统调用。

### 磁盘容量的边界（重要）

`/proc/mounts` 是 `self/mounts` 的软链，反映的是**读取进程的 mount namespace**，不是宿主机全局挂载表。Docker 无法让容器共享宿主机 mount namespace，所以容器内的磁盘容量天然有边界：

- **能准确拿到的**：CPU、内存、Swap、负载、运行时间、块设备 IO（`/proc/diskstats` 是全局的）、`--network=host` 下的网卡与 TCP 连接，以及宿主机根分区容量。
- **受限的**：宿主机上单独挂载的数据盘（例如挂在 `/app/docker/data` 的 xfs），若没有作为独立挂载点传播进容器，`statfs` 得到的会是它所在目录所属文件系统（通常被算进根分区），而不是那块盘自己的容量。

程序已自动兼容挂载表的两种形态（`normalize`）：容器里带 `/host` 前缀的路径直接 `statfs` 并剥掉前缀展示，宿主机原始路径则补 `/host` 前缀后再 `statfs`；输出里始终是宿主机路径。`rslave` 只保证宿主机上**之后**新发生的挂载事件会传播进来，不保证历史挂载点被递归复制。要 100% 完整的磁盘视角，用上面的 **systemd 宿主机部署**。

### 最小化挂载（不想暴露整个根）

- 只关心 CPU / 内存 / 网络、不需要磁盘容量：去掉 `-v /:/host:ro,rslave` 和 `-host-root`。`statfs` 失败的挂载点会被自动跳过，`mounts` 只列出容器内可见路径。
- 只想精确监控某几个宿主机目录：把它们挂到**与宿主机同名**的路径下，并且**不要设 `-host-root`**（否则程序会再去补 `/host` 前缀）：

```bash
-v /proc:/host/proc:ro -v /app:/app:ro -v /var/lib/docker:/var/lib/docker:ro
```

此时程序直接对 `/app`、`/var/lib/docker` 做 `statfs`，输出仍是宿主机路径。代价是主机名会显示容器 ID、OS 显示 `linux`；加 `--uts=host` 可让主机名正确（仍不需要 root）。也可以单独挂 `-v /etc/os-release:/etc/os-release:ro` 补发行版信息。

### 常见坑

| 现象 | 原因 / 处理 |
|---|---|
| 页面打不开 / `connection refused` | bridge 网络下绑了 `127.0.0.1`；改 `0.0.0.0:9100` 并配 `ports`，或直接用 `network_mode: host` |
| `mounts` 为空，或数值全是容器 overlay | 少了 `-v /:/host:ro,rslave` 或 `-host-root /host`；也可能是 SELinux 拦截 |
| 数据盘容量显示成根分区的值 | 该挂载点没传播进容器，见「磁盘容量的边界」；单独挂到同名路径，或改用 systemd |
| 网络只有 `eth0`、TCP 连接数偏小 | 少了 `--network=host` |
| 主机名显示容器 ID、OS 显示 `linux` 或镜像发行版 | 少了根挂载 `/:/host:ro,rslave`（程序从 `/host/etc/hostname`、`/host/etc/os-release` 读）；或加 `--uts=host` + 单独挂 `/etc/os-release` |
| 日志时间与北京时间差 8 小时 | scratch 无 tzdata，挂 `/usr/share/zoneinfo` 并设 `TZ` |
| `-log` 报 `read-only file system` / `permission denied` | `--read-only` 下需 `tmpfs` 或可写卷，且目录允许 uid 65532 写（compose 里已给注释模板） |
| `docker inspect` 里能看到令牌 | 改用 `HOSTMON_WEB_TOKEN` 环境变量，不要写进 `command` |
| Docker Desktop / rootless Docker 数据不对 | 拿到的是虚拟机或用户命名空间的视角，不是真实宿主机 |

## 数据来源与算法

| 指标 | 数据源 | 算法 |
|---|---|---|
| CPU 总/每核使用率、细分 | `/proc/stat` | 相邻两次采样的 jiffies 差值；`usage = 100 - Δidle%`（与 top / node_exporter 一致，iowait 计入非空闲并单独列分项）；含 user/nice/system/iowait/irq/softirq/steal |
| 内存 / Swap | `/proc/meminfo` | `used = MemTotal - MemAvailable`；老内核无 `MemAvailable` 时用 `Free+Buffers+Cached+SReclaimable-Shmem` 回退 |
| 负载 / 进程 | `/proc/loadavg` | load1/5/15、running/total；`PerCore1 = load1 / 核数` |
| 运行时间 | `/proc/uptime` + `/proc/stat` 的 `btime` | 秒数与 UTC 启动时刻 |
| 挂载点容量 / inode | `/proc/mounts` + `statfs(2)` | 白名单过滤伪文件系统；同一设备（docker overlay 等）去重，保留最短挂载点 |
| 磁盘 IOPS/吞吐/busy/延迟 | `/proc/diskstats` | 差分；sector 固定 512B；`util = Δio_time / Δt`；只取前 14 列，兼容 4.18+ 新增的 discard/flush 列 |
| 网卡速率/错包/丢包 | `/proc/net/dev` | 差分；默认排除 `lo`/`veth*`/`cni*` 等虚拟接口 |
| TCP 连接状态 | `/proc/net/tcp`, `/proc/net/tcp6` | 按状态字段聚合 |
| 主机信息 | `uname`、`/proc/cpuinfo`、`/proc/sys/kernel/osrelease`、`/etc/os-release` | ARM 无 `model name` 时回退 `Hardware`/`Processor` |

**为什么所有速率都基于两次采样**：`/proc/stat`、`/proc/net/dev`、`/proc/diskstats` 都是开机以来的累计计数器，单次读取只能得到总量，除以 uptime 是"历史平均值"而不是实时值。`Sampler` 用 `cur.At - prev.At` 作为真实时间窗口，因此改变 `-interval` 不影响结果正确性；计数器回绕/设备重置时做了防下溢保护。

## 注意事项

- **宿主机 systemd 最简单**；容器化同样能完整采集，但必须按「Docker 部署」章节挂载 `/proc` 与 `/`、设置 `-proc-root` / `-host-root` 并用 `--network=host`，否则 `statfs(2)` 与 `/proc/net/*` 会给出"宿主机 CPU + 容器网络"的混合视角。
- 无需 root，只读 `/proc`；`ProtectSystem=strict` 下也能正常工作。
- `-interval` 太小（如 100ms）会受 10ms jiffy 分辨率影响产生量化误差，生产建议 2~15s。
- 默认过滤虚拟网卡、按设备去重 overlay 挂载点，是为了避免 18+ 容器环境下刷屏；需要全量时用 `-net-all` / `-all-lines`。
- 与 `node_exporter` 可以共存：本程序更轻（单文件、可定制告警与 JSONL），若已有完整 exporter 体系，可只用 `-format json` 做一次性巡检或把 `/metrics` 作为补充。

## 开发验证

```bash
go vet ./...
go test ./...                 # 解析/差分/路径映射/告警状态机/Prometheus 格式/HTTP 端点共 21 组测试
bash build.sh                 # 交叉编译并输出 SHA256
```
