# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /converter .

FROM python:3.13-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        libgl1 \
        libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*
# Python deps first — stable layer, not affected by Go changes
COPY mokuro/ /mokuro/
RUN pip install --no-cache-dir /mokuro && \
    cp -r /mokuro/mokuro/assets \
      "$(python3 -c 'import importlib.util,pathlib; print(pathlib.Path(importlib.util.find_spec("mokuro").origin).parent)')"
# Go binary last — changes frequently, only invalidates this one layer
COPY --from=builder /converter /converter
ENTRYPOINT ["/converter"]
