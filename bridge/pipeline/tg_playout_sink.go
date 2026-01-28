package pipeline

import (
	"fmt"

	msdk "github.com/livekit/media-sdk"

	"gotgcalls/bridge/pcm"
)

// tgPlayoutSink receives decoded PCM16 samples from media-sdk pipeline,
// converts (optional) channel layout, chunks into TG frames and writes into playout buffer.
type tgPlayoutSink struct {
	sampleRate int

	inCh  int
	outCh int

	frameAssembler *pcm.FrameAssembler
	outFrameSize   int
	out            *pcm.PCMPlayoutBuffer

	// scratch
	tmp  msdk.PCM16Sample
	b    []byte
}

func newTGPlayoutSink(sampleRate int, inCh int, outCh int, outFrameSize int, out *pcm.PCMPlayoutBuffer) *tgPlayoutSink {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if inCh <= 0 {
		inCh = 1
	}
	if outCh <= 0 {
		outCh = 1
	}
	if outFrameSize <= 0 {
		outFrameSize = 1
	}
	return &tgPlayoutSink{
		sampleRate:     sampleRate,
		inCh:           inCh,
		outCh:          outCh,
		outFrameSize:   outFrameSize,
		frameAssembler: pcm.NewFrameAssembler(outFrameSize),
		out:            out,
	}
}

func (w *tgPlayoutSink) String() string {
	return fmt.Sprintf("TGPlayoutSink(%dHz %dch->%dch)", w.sampleRate, w.inCh, w.outCh)
}

func (w *tgPlayoutSink) SampleRate() int { return w.sampleRate }

func (w *tgPlayoutSink) WriteSample(sample msdk.PCM16Sample) error {
	// Convert channels if needed.
	if w.inCh != w.outCh {
		w.tmp = pcm.PCM16ConvertChannels(w.tmp, sample, w.inCh, w.outCh)
		sample = w.tmp
	}
	// int16 -> bytes (PCM16LE)
	w.b = pcm.PCM16SampleToBytes(w.b, sample)

	frames := w.frameAssembler.Push(w.b)
	for _, frame := range frames {
		w.out.WriteFrame(frame)
	}
	return nil
}
