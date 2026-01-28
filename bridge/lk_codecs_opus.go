//go:build (opus || with_opus_c) && cgo

package bridge

import (
	"strings"

	msdk "github.com/livekit/media-sdk"
	msdkopus "github.com/livekit/media-sdk/opus"
	msdkrtp "github.com/livekit/media-sdk/rtp"
	"github.com/livekit/protocol/logger"
)

// Register Opus codec into media-sdk registry for SIP usage.
//
// Enable with: `-tags opus` (requires libopus + pkg-config).
func init() {
	log := logger.GetLogger()

	register := func(sdpName string, channels int) {
		msdk.RegisterCodec(msdkrtp.NewAudioCodec(msdk.CodecInfo{
			SDPName:      sdpName,
			SampleRate:   48000,
			RTPClockRate: 48000,
			// Opus payload type is dynamic (96+), so RTPIsStatic stays false.
			// Prefer Opus over G722/G711 in negotiation.
			Priority: 100,
			FileExt:  "opus",
		}, func(w msdk.PCM16Writer) msdk.WriteCloser[msdkopus.Sample] {
			dec, err := msdkopus.Decode(w, channels, log)
			if err != nil {
				panic(err)
			}
			return &opusWriterWrap[msdkopus.Sample]{inner: dec}
		}, func(w msdk.WriteCloser[msdkopus.Sample]) msdk.PCM16Writer {
			enc, err := msdkopus.Encode(w, channels, log)
			if err != nil {
				panic(err)
			}
			return &opusPCM16WriterWrap{inner: enc}
		}))
	}

	// IMPORTANT: do not register "opus/48000".
	// media-sdk/sdp auto-aliases "<name>/<rate>" -> "<name>/<rate>/1".
	register("opus/48000/2", 2)
	register("opus/48000/1", 1)
}

// Match casing used by other codecs in pipeline strings.
type opusWriterWrap[S any] struct{ inner msdk.WriteCloser[S] }

func (w *opusWriterWrap[S]) String() string {
	return strings.Replace(w.inner.String(), "OPUS(", "opus(", 1)
}
func (w *opusWriterWrap[S]) SampleRate() int       { return w.inner.SampleRate() }
func (w *opusWriterWrap[S]) Close() error          { return w.inner.Close() }
func (w *opusWriterWrap[S]) WriteSample(s S) error { return w.inner.WriteSample(s) }

type opusPCM16WriterWrap struct{ inner msdk.PCM16Writer }

func (w *opusPCM16WriterWrap) String() string {
	return strings.Replace(w.inner.String(), "OPUS(", "opus(", 1)
}
func (w *opusPCM16WriterWrap) SampleRate() int                      { return w.inner.SampleRate() }
func (w *opusPCM16WriterWrap) Close() error                         { return w.inner.Close() }
func (w *opusPCM16WriterWrap) WriteSample(s msdk.PCM16Sample) error { return w.inner.WriteSample(s) }
