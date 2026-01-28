package pipeline

import (
	"sync/atomic"
	"time"

	msdk "github.com/livekit/media-sdk"
	msdkrtp "github.com/livekit/media-sdk/rtp"
	"github.com/livekit/protocol/logger"
	prtp "github.com/pion/rtp"
)

// silenceFiller detects RTP timestamp discontinuities (DTX/silence suppression)
// and generates silence samples to fill the gaps before passing packets to the decoder.
//
// This is adapted from LiveKit SIP implementation, but kept local to this bridge.
type silenceFiller struct {
	maxGapSize      int
	encodedSink     msdkrtp.Handler
	pcmSink         msdk.PCM16Writer
	samplesPerFrame int
	log             logger.Logger
	lastTS          atomic.Uint64
	lastSeq         atomic.Uint64
	packets         atomic.Uint64
}

func newSilenceFiller(encodedSink msdkrtp.Handler, pcmSink msdk.PCM16Writer, clockRate int, log logger.Logger) msdkrtp.Handler {
	// media-sdk assumes 20ms frame duration (rtp.DefFrameDur).
	return &silenceFiller{
		maxGapSize:      25,
		encodedSink:     encodedSink,
		pcmSink:         pcmSink,
		samplesPerFrame: clockRate / msdkrtp.DefFramesPerSec,
		log:             log,
	}
}

func (h *silenceFiller) String() string {
	return "SilenceFiller -> " + h.encodedSink.String()
}

func (h *silenceFiller) isSilenceSuppression(header *prtp.Header) (bool, int) {
	packets := h.packets.Add(1)
	lastSeq := uint16(h.lastSeq.Swap(uint64(header.SequenceNumber)))
	lastTS := uint32(h.lastTS.Swap(uint64(header.Timestamp)))
	if packets == 1 {
		return false, 0
	}

	expectedSeq := lastSeq + 1
	expectedTS := lastTS + uint32(h.samplesPerFrame)

	seqDiff := header.SequenceNumber - expectedSeq
	tsDiff := header.Timestamp - expectedTS

	// A key characteristic of DTX is no sequence gaps, but >1 frame TS gaps.
	if seqDiff != 0 {
		return false, 0
	}

	missedFrames := int(tsDiff) / int(h.samplesPerFrame)
	if missedFrames == 0 {
		return false, 0
	}
	return true, missedFrames
}

func (h *silenceFiller) fillWithSilence(framesToFill int) error {
	for ; framesToFill > 0; framesToFill-- {
		silence := make(msdk.PCM16Sample, h.samplesPerFrame)
		if err := h.pcmSink.WriteSample(silence); err != nil {
			return err
		}
	}
	return nil
}

func (h *silenceFiller) HandleRTP(header *prtp.Header, payload []byte) error {
	isDTX, missingFrameCount := h.isSilenceSuppression(header)
	if isDTX && missingFrameCount <= h.maxGapSize*100 {
		// Avoid flooding in case this is actually a reset.
		if missingFrameCount <= h.maxGapSize {
			if err := h.fillWithSilence(missingFrameCount); err != nil {
				return err
			}
		} else if h.log != nil && time.Now().Unix()%15 == 0 {
			h.log.Infow("large timestamp gap (ignored)", "gapFrames", missingFrameCount)
		}
	}
	return h.encodedSink.HandleRTP(header, payload)
}
