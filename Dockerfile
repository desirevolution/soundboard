FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod VERSION ./
COPY *.go ./
COPY web/ ./web/
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/soundboard .

FROM alpine:3.22
RUN apk add --no-cache ffmpeg ca-certificates \
    && addgroup -S soundboard && adduser -S -G soundboard soundboard \
    && mkdir -p /data/sounds && chown -R soundboard:soundboard /data
COPY --from=build /out/soundboard /usr/local/bin/soundboard
USER soundboard
ENV LISTEN_ADDR=:8080 SOUNDS_DIR=/data/sounds
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s \
    CMD wget -q -O - http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/soundboard"]
