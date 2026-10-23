# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app && mkdir /data && chown app /data
COPY --from=build /out/ /usr/local/bin/
USER app
ENV INDEX_DIR=/data/index DATA_DIR=/data CRAWL_OUT=/data/pages.jsonl ADDR=:8080
EXPOSE 8080
VOLUME /data
CMD ["server"]
