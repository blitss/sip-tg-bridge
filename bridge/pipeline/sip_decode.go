package pipeline

import (
	msdk "github.com/livekit/media-sdk"
	msdkrtp "github.com/livekit/media-sdk/rtp"
	"github.com/livekit/protocol/logger"

	"gotgcalls/bridge/pcm"
)

type SipDecodeConfig struct {
	Codec         msdkrtp.AudioCodec
	PayloadType   uint8
	InputChannels int
	OutputFormat  pcm.AudioFormat
	PlayoutBuffer *pcm.PCMPlayoutBuffer
	EnableJitter  bool
	Log           logger.Logger
}

func BuildSipDecodeChain(cfg SipDecodeConfig) (msdkrtp.HandlerCloser, error) {
	if cfg.Codec == nil {
		return nil, errInvalid("codec")
	}
	if cfg.PlayoutBuffer == nil {
		return nil, errInvalid("playout buffer")
	}
	if cfg.OutputFormat.SampleRate <= 0 {
		cfg.OutputFormat.SampleRate = 48000
	}
	if cfg.OutputFormat.Channels <= 0 {
		cfg.OutputFormat.Channels = 1
	}
	if cfg.OutputFormat.FrameDur <= 0 {
		cfg.OutputFormat.FrameDur = msdkrtp.DefFrameDur
	}

	outFrameSize := cfg.OutputFormat.FrameBytes()
	sink := newTGPlayoutSink(cfg.OutputFormat.SampleRate, cfg.InputChannels, cfg.OutputFormat.Channels, outFrameSize, cfg.PlayoutBuffer)
	pcmSink := msdk.NopCloser[msdk.PCM16Sample](sink)

	info := cfg.Codec.Info()
	clockRate := info.RTPClockRate

	var h msdkrtp.Handler = cfg.Codec.DecodeRTP(sink, cfg.PayloadType)
	h = newSilenceFiller(h, pcmSink, clockRate, cfg.Log)
	var hc msdkrtp.HandlerCloser = msdkrtp.NewNopCloser(h)
	if cfg.EnableJitter {
		hc = msdkrtp.HandleJitter(hc)
	}
	return hc, nil
}

type invalidConfig struct {
	field string
}

func (e invalidConfig) Error() string {
	return "invalid " + e.field
}

func errInvalid(field string) error {
	return invalidConfig{field: field}
}
