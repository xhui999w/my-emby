# syntax=docker/dockerfile:1
# ---- 构建阶段 ----
FROM golang:1.22-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off
# lib/pq 已置于 third_party 并通过 go.mod replace 引用，无需联网
COPY go.mod ./
COPY third_party ./third_party
COPY *.go ./
COPY web ./web
RUN go build -trimpath -ldflags "-s -w" -o /out/go-emby .

# ---- 运行阶段 ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/go-emby /app/go-emby
ENV HTTP_PORT=8097 \
    MEDIA_ROOTS=/media \
    APP_DATA_PATH=/app/data \
    TZ=Asia/Shanghai
RUN mkdir -p /app/data /media
VOLUME ["/app/data", "/media"]
EXPOSE 8097
ENTRYPOINT ["/app/go-emby"]
