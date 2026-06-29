# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /yomekuro ./cmd/yomekuro

FROM gcr.io/distroless/static-debian12
COPY --from=builder /yomekuro /yomekuro
EXPOSE 8080
ENTRYPOINT ["/yomekuro"]
