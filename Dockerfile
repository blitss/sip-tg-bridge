# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder

WORKDIR /src

# System build deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    make \
    pkg-config \
    python3 \
    python3-pip \
    build-essential \
    libopus-dev \
    libopusfile-dev \
    libsoxr-dev \
    libasound2-dev \
  && rm -rf /var/lib/apt/lists/*

# CMake >= 3.27 required by ntgcalls. Install via pip to keep it recent.
RUN pip3 install --no-cache-dir --break-system-packages cmake cmake

# Copy sources
COPY . .

# Build everything (skip submodule update in Docker; assume sources are present)
RUN make -o submodules build-ntgcalls && make build-bridge

FROM debian:bookworm-slim AS runtime

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libopus0 \
    libopusfile0 \
    libsoxr0 \
  && rm -rf /var/lib/apt/lists/*

# Runtime dynamic libs
ENV LD_LIBRARY_PATH=/app/lib

COPY --from=builder /src/bin/sip-tg-bridge /app/sip-tg-bridge
COPY --from=builder /src/lib/libntgcalls.so /app/lib/libntgcalls.so

EXPOSE 5060/tcp
EXPOSE 5060/udp

ENTRYPOINT ["/app/sip-tg-bridge"]
CMD ["/app/config.yaml"]

