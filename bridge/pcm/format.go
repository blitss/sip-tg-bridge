package pcm

import "time"

// AudioFormat describes PCM16 audio framing.
type AudioFormat struct {
	SampleRate int
	Channels   int
	FrameDur   time.Duration
}

func (f AudioFormat) FrameSamples() int {
	sr := f.SampleRate
	if sr < 1 {
		sr = 1
	}
	ch := f.Channels
	if ch < 1 {
		ch = 1
	}
	return int(float64(sr) * f.FrameDur.Seconds() * float64(ch))
}

func (f AudioFormat) FrameBytes() int {
	return f.FrameSamples() * 2
}
