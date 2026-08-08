# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /marketplace ./cmd/marketplace

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /marketplace /app/marketplace
ENV HTTP_ADDR=:8080
ENV STORAGE_TYPE=local
ENV STORAGE_LOCAL_ROOT=/data
EXPOSE 8080
VOLUME ["/data"]
USER nobody
ENTRYPOINT ["/app/marketplace"]
