package pcm

import (
	"encoding/binary"

	msdk "github.com/livekit/media-sdk"
)

func PCM16BytesToSample(dst msdk.PCM16Sample, src []byte) msdk.PCM16Sample {
	n := len(src) / 2
	if cap(dst) < n {
		dst = make(msdk.PCM16Sample, n)
	} else {
		dst = dst[:n]
	}
	for i := 0; i < n; i++ {
		dst[i] = int16(binary.LittleEndian.Uint16(src[i*2 : i*2+2]))
	}
	return dst
}

func PCM16SampleToBytes(dst []byte, src msdk.PCM16Sample) []byte {
	need := len(src) * 2
	if cap(dst) < need {
		dst = make([]byte, need)
	} else {
		dst = dst[:need]
	}
	for i, s := range src {
		binary.LittleEndian.PutUint16(dst[i*2:i*2+2], uint16(s))
	}
	return dst
}

func PCM16ConvertChannels(dst msdk.PCM16Sample, src msdk.PCM16Sample, inCh int, outCh int) msdk.PCM16Sample {
	if inCh <= 0 {
		inCh = 1
	}
	if outCh <= 0 {
		outCh = 1
	}
	if inCh == outCh {
		if cap(dst) < len(src) {
			dst = make(msdk.PCM16Sample, len(src))
		} else {
			dst = dst[:len(src)]
		}
		copy(dst, src)
		return dst
	}
	// Only support mono<->stereo for now (matches current bridge assumptions).
	if inCh == 2 && outCh == 1 {
		n := len(src) / 2
		if cap(dst) < n {
			dst = make(msdk.PCM16Sample, n)
		} else {
			dst = dst[:n]
		}
		for i := 0; i < n; i++ {
			l := int32(src[i*2])
			r := int32(src[i*2+1])
			dst[i] = int16((l + r) / 2)
		}
		return dst
	}
	if inCh == 1 && outCh == 2 {
		n := len(src) * 2
		if cap(dst) < n {
			dst = make(msdk.PCM16Sample, n)
		} else {
			dst = dst[:n]
		}
		for i := 0; i < len(src); i++ {
			v := src[i]
			dst[i*2] = v
			dst[i*2+1] = v
		}
		return dst
	}
	// Fallback: best effort (truncate to minimum whole frames).
	frames := len(src) / inCh
	n := frames * outCh
	if cap(dst) < n {
		dst = make(msdk.PCM16Sample, n)
	} else {
		dst = dst[:n]
	}
	// naive: just copy first channel into all outputs
	for f := 0; f < frames; f++ {
		v := src[f*inCh]
		for c := 0; c < outCh; c++ {
			dst[f*outCh+c] = v
		}
	}
	return dst
}

type PCM16Assembler struct {
	frameSamples int
	buf          msdk.PCM16Sample
}

func NewPCM16Assembler(frameSamples int) *PCM16Assembler {
	if frameSamples < 1 {
		frameSamples = 1
	}
	return &PCM16Assembler{frameSamples: frameSamples}
}

func (a *PCM16Assembler) Push(in msdk.PCM16Sample) []msdk.PCM16Sample {
	if len(in) == 0 {
		return nil
	}
	a.buf = append(a.buf, in...)
	var out []msdk.PCM16Sample
	for len(a.buf) >= a.frameSamples {
		frame := make(msdk.PCM16Sample, a.frameSamples)
		copy(frame, a.buf[:a.frameSamples])
		out = append(out, frame)
		a.buf = a.buf[a.frameSamples:]
	}
	return out
}
