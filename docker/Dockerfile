# --- Build stage ---
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.date=${BUILD_DATE}'" \
    -o /bin/akili ./cmd/akili

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1001 -h /home/akili akili

COPY --from=builder /bin/akili /usr/local/bin/akili

RUN mkdir -p /data && chown akili:akili /data

USER 1001
WORKDIR /home/akili

EXPOSE 8080

ENTRYPOINT ["akili"]
CMD ["--config", "/etc/akili/config.json"]
