# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/homelab-teach .

FROM alpine:3.20
RUN addgroup -g 10001 -S app && adduser -u 10001 -S -G app app
COPY --from=build /out/homelab-teach /usr/local/bin/homelab-teach
RUN mkdir -p /data && chown 10001:10001 /data

USER 10001:10001
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/homelab-teach"]
CMD ["-addr", "0.0.0.0:8080", "-db", "/data/app.db"]
