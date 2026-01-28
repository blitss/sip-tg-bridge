# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder

WORKDIR /src

# Install LLVM/Clang 21 from official LLVM repository
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    gnupg \
    lsb-release \
  && curl -fsSL https://apt.llvm.org/llvm-snapshot.gpg.key | gpg --dearmor -o /usr/share/keyrings/llvm-archive-keyring.gpg \
  && echo "deb [signed-by=/usr/share/keyrings/llvm-archive-keyring.gpg] http://apt.llvm.org/bookworm/ llvm-toolchain-bookworm-21 main" > /etc/apt/sources.list.d/llvm.list \
  && apt-get update \
  && apt-get install -y --no-install-recommends \
    git \
    make \
    pkg-config \
    python3 \
    python3-pip \
    build-essential \
    clang-21 \
    lld-21 \
    llvm-21 \
    libopus-dev \
    libopusfile-dev \
    libsoxr-dev \
    libasound2-dev \
    libpulse-dev \
    libglib2.0-dev \
    libpipewire-0.3-dev \
  && ln -sf /usr/bin/clang-21 /usr/bin/clang \
  && ln -sf /usr/bin/clang++-21 /usr/bin/clang++ \
  && ln -sf /usr/bin/lld-21 /usr/bin/lld \
  && rm -rf /var/lib/apt/lists/*

# CMake >= 3.27 required by ntgcalls. Install via pip to keep it recent.
RUN pip3 install --no-cache-dir --break-system-packages cmake cmake

# Set Clang as default compiler
ENV CC=clang
ENV CXX=clang++

# Copy sources
COPY . .

# Clean any stale build artifacts from host
RUN rm -rf ntgcalls/build_lib ntgcalls/shared-output ntgcalls/static-output

# Build everything (skip submodule update in Docker; assume sources are present)
RUN make -o submodules build-ntgcalls && make build-bridge

FROM debian:bookworm-slim AS runtime

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libopus0 \
    libopusfile0 \
    libsoxr0 \
    libpulse0 \
    libpipewire-0.3-0 \
  && rm -rf /var/lib/apt/lists/*

# Runtime dynamic libs
ENV LD_LIBRARY_PATH=/app/lib

COPY --from=builder /src/bin/sip-tg-bridge /app/sip-tg-bridge
COPY --from=builder /src/lib/libntgcalls.so /app/lib/libntgcalls.so

EXPOSE 5060/tcp
EXPOSE 5060/udp

ENTRYPOINT ["/app/sip-tg-bridge"]
CMD ["/app/config.yaml"]

