CGO_ENABLED ?= 1
BIN_DIR ?= bin
LIB_DIR ?= lib
BRIDGE_BIN ?= $(BIN_DIR)/sip-tg-bridge
GO_TAGS ?= soxr,with_opus_c,opus
PYTHON ?= python3

NTGCALLS_DIR := ntgcalls
NTGCALLS_OUTPUT := $(NTGCALLS_DIR)/shared-output

# Detect library extension
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
    LIB_EXT := dylib
    PKG_MANAGER := brew
    INSTALL_CMD := brew install pkg-config opus opusfile libsoxr pulseaudio
else ifeq ($(UNAME_S),Linux)
    LIB_EXT := so
    PKG_MANAGER := apt
    INSTALL_CMD := sudo apt-get install pkg-config libopus-dev libopusfile-dev libsoxr-dev libpulse-dev libglib2.0-dev libpipewire-0.3-dev
else
    LIB_EXT := dll
    PKG_MANAGER := unknown
    INSTALL_CMD := (please install manually)
endif

.PHONY: all build-bridge run-bridge build-ntgcalls clean clean-ntgcalls submodules check-deps

all: build-bridge

# Check required libraries via pkg-config
check-deps:
	@command -v pkg-config >/dev/null 2>&1 || { \
		echo "Error: pkg-config is not installed."; \
		echo "Install with: $(INSTALL_CMD)"; \
		exit 1; \
	}
	@pkg-config --exists opus || { \
		echo "Error: libopus is not installed."; \
		echo "Install with: $(INSTALL_CMD)"; \
		exit 1; \
	}
	@pkg-config --exists opusfile || { \
		echo "Error: libopusfile is not installed."; \
		echo "Install with: $(INSTALL_CMD)"; \
		exit 1; \
	}
	@pkg-config --exists soxr || { \
		echo "Error: libsoxr is not installed."; \
		echo "Install with: $(INSTALL_CMD)"; \
		exit 1; \
	}
	@echo "✓ All required libraries found (opus, opusfile, soxr)"

submodules:
	git -c submodule.fetchJobs=4 submodule update --init --recursive --depth 1 --recommend-shallow

# Build ntgcalls via CMake (avoid editing submodule)
build-ntgcalls: submodules
	cd $(NTGCALLS_DIR) && cmake -S . -B build_lib \
		-DCMAKE_BUILD_TYPE=$(if $(filter $(UNAME_S),Linux),RelWithDebInfo,Release) \
		-DSTATIC_BUILD=OFF \
		-DIS_PYTHON=OFF \
		-DANDROID_ABI=OFF \
		-DPython_EXECUTABLE=$(PYTHON) \
		-DCMAKE_TOOLCHAIN_FILE=cmake/Toolchain.cmake \
		-DCMAKE_PROJECT_INCLUDE=$(abspath cmake/ntgcalls_visibility_fix.cmake)
	cd $(NTGCALLS_DIR) && cmake --build build_lib --config $(if $(filter $(UNAME_S),Linux),RelWithDebInfo,Release)
	mkdir -p $(LIB_DIR)
	cp $(NTGCALLS_OUTPUT)/lib/libntgcalls.$(LIB_EXT) $(LIB_DIR)/
	cp $(NTGCALLS_OUTPUT)/include/ntgcalls.h $(LIB_DIR)/

build-bridge: check-deps
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=$(CGO_ENABLED) go build -tags "$(GO_TAGS)" -o $(BRIDGE_BIN) ./cmd/sip-tg-bridge

build-all: build-ntgcalls build-bridge

run-bridge: check-deps
	CGO_ENABLED=$(CGO_ENABLED) go run -tags "$(GO_TAGS)" ./cmd/sip-tg-bridge

clean:
	rm -rf $(BIN_DIR)

clean-ntgcalls:
	rm -rf $(NTGCALLS_DIR)/build_lib $(NTGCALLS_OUTPUT)

clean-all: clean clean-ntgcalls
