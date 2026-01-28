package pipeline

import (
	msdk "github.com/livekit/media-sdk"
	msdkrtp "github.com/livekit/media-sdk/rtp"

	"github.com/emiago/diago/media"
)

type SipEncodeConfig struct {
	Codec       msdkrtp.AudioCodec
	PayloadType uint8
	RTPClock    int
	SourceRate  int
	RTPWriter   media.RTPWriter
}

type SipEncodePipeline struct {
	Writer msdk.PCM16Writer
	Delay  func(uint32)
}

func BuildSipEncodePipeline(cfg SipEncodeConfig) (*SipEncodePipeline, error) {
	if cfg.Codec == nil {
		return nil, errInvalid("codec")
	}
	if cfg.RTPWriter == nil {
		return nil, errInvalid("rtp writer")
	}
	info := cfg.Codec.Info()
	if cfg.RTPClock <= 0 {
		cfg.RTPClock = info.RTPClockRate
	}
	if cfg.SourceRate <= 0 {
		cfg.SourceRate = info.SampleRate
	}
	seq := msdkrtp.NewSeqWriter(&diagoRTPWriterAdapter{w: cfg.RTPWriter})
	stream := seq.NewStream(cfg.PayloadType, cfg.RTPClock)

	out := cfg.Codec.EncodeRTP(stream)
	out = msdk.ResampleWriter(out, cfg.SourceRate)

	return &SipEncodePipeline{
		Writer: out,
		Delay:  stream.Delay,
	}, nil
}
