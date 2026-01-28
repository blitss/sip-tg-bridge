package endpoints

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
	msdk "github.com/livekit/media-sdk"
	msdkrtp "github.com/livekit/media-sdk/rtp"
	msdksdp "github.com/livekit/media-sdk/sdp"

	"gotgcalls/bridge/pcm"
)

type SIPDialog interface {
	MediaSession() *media.MediaSession
	Media() *diago.DialogMedia
}

type SipEndpoint struct {
	// LKCodec is the LiveKit media-sdk codec implementation used for RTP encode/decode.
	LKCodec msdkrtp.AudioCodec
	// LKSDPName is the SDP codec name used to resolve LKCodec.
	LKSDPName string

	FrameSize int
	Codec     media.Codec

	// RTP IO (diago).
	rtpReader media.RTPReader
	rtpWriter media.RTPWriter

	// SampleRate is the decoded PCM sample rate for the codec (e.g. 16000 for G722, 8000 for G711, 48000 for Opus).
	SampleRate int
	// RTPClockRate is the RTP timestamp clock rate (e.g. 8000 for G722).
	RTPClockRate int
	// Channels is number of interleaved PCM channels in the codec stream (e.g. 2 for Opus stereo).
	Channels int

	FrameDur     time.Duration
	EnableJitter bool
}

type SIPMediaConfig struct {
	JitterMinPackets uint16
	FrameDuration    time.Duration
}

func NewSipEndpoint(dialog SIPDialog, cfg SIPMediaConfig) (*SipEndpoint, error) {
	session := dialog.MediaSession()
	if session == nil {
		return nil, errors.New("sip media session not ready")
	}
	// Pick the negotiated *audio* codec (ignore telephone-event which is DTMF-only).
	// diago's CodecAudioFromSession should already skip telephone-event, but when the
	// intersection ends up being DTMF-only (e.g. peer didn't offer any of our audio
	// codecs), we want a clear error instead of trying to start media with DTMF.
	pickAudio := func() (media.Codec, error) {
		if session == nil {
			return media.Codec{}, errors.New("sip media session not ready")
		}
		// Prefer common codecs after negotiation.
		if commons := session.CommonCodecs(); len(commons) > 0 {
			if c, ok := media.CodecAudioFromList(commons); ok {
				return c, nil
			}
			return media.Codec{}, fmt.Errorf("no audio codec negotiated (common codecs are DTMF-only): %v", commons)
		}
		// Fallback to session codec list.
		if c, ok := media.CodecAudioFromList(session.Codecs); ok {
			return c, nil
		}
		return media.Codec{}, errors.New("no audio codec negotiated")
	}
	codec, err := pickAudio()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(codec.Name) {
	case "opus", "pcmu", "pcma", "g722":
	default:
		return nil, fmt.Errorf("unsupported sip codec %q", codec.Name)
	}
	// Allow Opus stereo; require mono for the rest.
	if strings.ToLower(codec.Name) == "opus" {
		if codec.NumChannels != 1 && codec.NumChannels != 2 {
			return nil, fmt.Errorf("unsupported sip channel count %d", codec.NumChannels)
		}
	} else {
		if codec.NumChannels != 1 {
			return nil, fmt.Errorf("unsupported sip channel count %d", codec.NumChannels)
		}
	}

	rtpReader := dialog.Media().RTPPacketReader.Reader()
	rtpWriter := dialog.Media().RTPPacketWriter.Writer()

	// Map negotiated diago codec to media-sdk SDP name (canonicalized).
	sdpName := media.CanonicalSDPName(codec)
	if strings.TrimSpace(sdpName) == "" {
		return nil, fmt.Errorf("cannot map sip codec %q to media-sdk", codec.Name)
	}

	lk := msdksdp.CodecByName(sdpName)
	audioCodec, ok := lk.(msdkrtp.AudioCodec)
	if !ok || audioCodec == nil || !msdk.CodecEnabled(lk) {
		return nil, fmt.Errorf("media-sdk codec not available: %q (did you enable build tags / imports?)", sdpName)
	}

	info := audioCodec.Info()

	frameDur := cfg.FrameDuration
	if frameDur <= 0 {
		frameDur = 20 * time.Millisecond
	}

	return &SipEndpoint{
		LKCodec:      audioCodec,
		LKSDPName:    sdpName,
		FrameSize:    int(float64(info.SampleRate)*frameDur.Seconds()) * maxInt(1, codec.NumChannels) * 2,
		Codec:        codec,
		rtpReader:    rtpReader,
		rtpWriter:    rtpWriter,
		SampleRate:   info.SampleRate,
		RTPClockRate: info.RTPClockRate,
		Channels:     maxInt(1, codec.NumChannels),
		FrameDur:     frameDur,
		EnableJitter: cfg.JitterMinPackets > 0,
	}, nil
}

func (s *SipEndpoint) Close() {
	// no-op (media-sdk pipeline lives in bridge)
}

func (s *SipEndpoint) PayloadType() uint8 {
	return uint8(s.Codec.PayloadType)
}

func (s *SipEndpoint) RTPReader() media.RTPReader {
	return s.rtpReader
}

func (s *SipEndpoint) RTPWriter() media.RTPWriter {
	return s.rtpWriter
}

func (s *SipEndpoint) Format() pcm.AudioFormat {
	return pcm.AudioFormat{
		SampleRate: s.SampleRate,
		Channels:   s.Channels,
		FrameDur:   s.FrameDur,
	}
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
