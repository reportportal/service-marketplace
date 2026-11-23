# Build stage
FROM golang:1.27.4-alpine3.24 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /marketplace ./cmd/marketplace
# Distroless has no shell, so create the local-storage root here, owned by the
# image's nonroot user (65532).
RUN mkdir /data && chown 65532:65532 /data

# Runtime stage: distroless static (no shell, no package manager) with CA
# certificates and tzdata built in. The tag tracks Debian security rebuilds.
FROM gcr.io/distroless/static-debian13:nonroot
ARG APP_VERSION=
ARG BUILD_BRANCH=
ARG BUILD_DATE=
LABEL org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.ref.name="${BUILD_BRANCH}" \
      org.opencontainers.image.created="${BUILD_DATE}"
WORKDIR /app
COPY --from=builder /marketplace /app/marketplace
COPY --from=builder --chown=65532:65532 /data /data
ENV HTTP_ADDR=:8080
ENV STORAGE_TYPE=local
ENV STORAGE_LOCAL_ROOT=/data
EXPOSE 8080
VOLUME ["/data"]
USER 65532:65532
ENTRYPOINT ["/app/marketplace"]
