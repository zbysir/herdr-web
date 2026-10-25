# herdr-relay 的镜像（ghcr.io/zbysir/herdr-relay），由 goreleaser 的 dockers_v2 出，别手动 build。
# 二进制是 goreleaser 交叉编译好的那份，按平台放在 build context 的 linux/amd64/、linux/arm64/ 下，
# 所以这里只有 COPY、没有 RUN —— 出 arm64 镜像不用 QEMU。
#
# alpine 而不是 scratch：控制面不配证书时中继自己去 Let's Encrypt 签（TLS-ALPN-01），
# 要系统根证书；alpine 自带 ca-certificates-bundle，还留着一个 sh 方便 docker exec 进去看。
FROM alpine:3.20
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/herdr-relay /usr/local/bin/herdr-relay
# 数据（主机公钥表、邀请码、控制面证书）放在卷上：容器重建时注册过的主机不能丢
ENV HERDR_RELAY_DIR=/data
VOLUME /data
EXPOSE 443
ENTRYPOINT ["/usr/local/bin/herdr-relay"]
