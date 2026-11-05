# syntax=docker/dockerfile:1
# 构建前先执行 `bash build.sh`（或手工 go build）产出 dist/ 下的静态二进制。
# 镜像里只有这一个 ELF：无 shell、无 libc、无包管理器，攻击面最小。
#
# 默认拷贝 arm64 二进制（阿里云 Anolis OS aarch64）；x86_64 服务器改用：
#   docker build --build-arg HOSTMON_BIN=dist/hostmon-linux-amd64 -t hostmon:local .
FROM scratch
ARG HOSTMON_BIN=dist/hostmon-linux-arm64
COPY --chmod=0755 ${HOSTMON_BIN} /hostmon

# 采集只读 /proc 与 statfs(2)，不需要 root，也不需要任何 capability。
USER 65532:65532
EXPOSE 9100
ENTRYPOINT ["/hostmon"]
# compose 会用 command 覆盖；单独 docker run 时默认监听容器内所有地址。
CMD ["-web", "0.0.0.0:9100", "-interval", "5s"]
