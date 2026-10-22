FROM ghcr.io/felix2yu/base:alpine-3.24

WORKDIR /app
# 基镜会按 PUID/PGID 对该目录做 chown -R，SQLite 的 WAL 需要对目录本身可写
ENV APP_DATA_DIR=/app/data
ENV TZ=Asia/Shanghai

COPY --chmod=755 bin/siwu /app/siwu

EXPOSE 8788

# 探活端点必须免鉴权：配了 SIWU_PASSWORD 之后其它端点都要登录
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O- http://127.0.0.1:8788/api/healthz >/dev/null || exit 1

CMD ["/app/siwu", "serve", "--addr", ":8788", "--data", "/app/data"]
