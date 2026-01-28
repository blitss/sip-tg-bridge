# SIP-Telegram Bridge

> **POC / Work in Progress** - This is an experimental project created as substitute for [SIP.TG](https://www.sip.tg).and other deprecated projects, like https://github.com/Infactum/tg2sip (uses WebRtc)

A bridge that connects SIP telephony with Telegram voice calls. Make and receive phone calls through your Telegram account using any SIP provider.

Based on [LiveKit SIP](https://github.com/livekit/sip) audio pipeline architecture. LiveKit's SIP service provides a battle-tested foundation for SIP/RTP handling and audio transcoding, which this project adapts for direct Telegram integration instead of WebRTC rooms.

## Features

- Receive incoming SIP calls as Telegram voice calls
- Initiate outbound calls via Telegram command (`/call +79991234567`)
- Audio transcoding (Opus, PCMU, PCMA)
- DTMF support (RFC2833)
- SIP registration with authentication

## Prerequisites

**Go >= 1.18** is required.

The bridge uses native audio libraries that must be installed:

**Debian/Ubuntu:**
```bash
sudo apt-get install pkg-config libopus-dev libopusfile-dev libsoxr-dev libpulse-dev libglib2.0-dev libpipewire-0.3-dev
```

**macOS:**
```bash
brew install pkg-config opus opusfile libsoxr pulseaudio
```

## Build

```bash
# Clone with submodules
git clone --recursive <repo-url>
cd worker

# Build the bridge
make build-bridge

# Or build everything including ntgcalls library
make build-all
```

## Configuration

Copy and edit `config.yaml`:

```yaml
telegram:
  # Get from https://my.telegram.org
  app_id: YOUR_APP_ID
  app_hash: "YOUR_APP_HASH"
  # Your Telegram user ID
  user_id: 123456789

sip:
  provider_host: "sip.provider.com"
  bind_port: 5060
  auth_user: "your_sip_user"
  auth_password: "your_sip_pass"
  external_ip: "your.public.ip"
```

## Usage

```bash
# Run the bridge
make run-bridge

# Or run directly
./bin/sip-tg-bridge config.yaml
```

Once running:
- Incoming SIP calls will ring your Telegram account
- Send `/call +79991234567` to your bot to initiate outbound calls

## Status

This project is a **proof of concept** and **work in progress**. Expect bugs and missing features.

## License

Apache-2.0
