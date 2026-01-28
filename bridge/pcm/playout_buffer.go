package pcm

import "sync"

// PCMPlayoutBuffer is a simple byte FIFO for fixed-size PCM frames.
//
// Goal: decouple bursty PCM production (SIP decoder) from real-time consumption
// (TG 10ms pacing). This is the PCM equivalent of "buffer + silence filler".
//
// It does NOT do time-stretching. Underflow => outputs silence. Overflow =>
// drop oldest frames (bounded elsewhere).
type PCMPlayoutBuffer struct {
	frameSize int

	mu  sync.Mutex
	buf []byte
}

func NewPCMPlayoutBuffer(frameSize int) *PCMPlayoutBuffer {
	if frameSize < 1 {
		frameSize = 1
	}
	return &PCMPlayoutBuffer{
		frameSize: frameSize,
		// keep a little headroom; grows if needed
		buf: make([]byte, 0, frameSize*50),
	}
}

func (b *PCMPlayoutBuffer) FrameSize() int { return b.frameSize }

func (b *PCMPlayoutBuffer) LenFrames() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf) / b.frameSize
}

// WriteFrame appends exactly one frame. If size mismatches, it is ignored.
func (b *PCMPlayoutBuffer) WriteFrame(frame []byte) {
	if len(frame) != b.frameSize {
		return
	}
	b.mu.Lock()
	b.buf = append(b.buf, frame...)
	b.mu.Unlock()
}

// DropFrames drops up to n oldest frames and returns how many were dropped.
func (b *PCMPlayoutBuffer) DropFrames(n int) int {
	if n <= 0 {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	available := len(b.buf) / b.frameSize
	if available <= 0 {
		return 0
	}
	if n > available {
		n = available
	}
	b.buf = b.buf[n*b.frameSize:]
	return n
}

// ReadInto writes one frame into dst.
// Returns ok=false if there wasn't enough data (dst filled with zeros).
func (b *PCMPlayoutBuffer) ReadInto(dst []byte) (ok bool) {
	return b.ReadIntoAdjust(dst, 0)
}

// ReadIntoAdjust outputs exactly one frame into dst, but can slightly adjust
// consumption from the buffer by +/-1 PCM16 sample to correct drift without
// dropping whole frames.
//
// adjustSamples:
// -  0: consume exactly frameSize bytes
// - +1: consume frameSize+2 bytes (time-compress by dropping 1 sample)
// - -1: consume frameSize-2 bytes (time-expand by duplicating 1 sample)
//
// Returns ok=false if there wasn't enough data; dst is filled with zeros.
func (b *PCMPlayoutBuffer) ReadIntoAdjust(dst []byte, adjustSamples int) (ok bool) {
	if len(dst) != b.frameSize {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	// We only support tiny adjustments; anything else is ignored.
	if adjustSamples > 1 {
		adjustSamples = 1
	} else if adjustSamples < -1 {
		adjustSamples = -1
	}

	// PCM16 => 2 bytes/sample. If frameSize isn't even, fall back to exact copy.
	if b.frameSize%2 != 0 {
		if len(b.buf) < b.frameSize {
			for i := range dst {
				dst[i] = 0
			}
			return false
		}
		copy(dst, b.buf[:b.frameSize])
		b.buf = b.buf[b.frameSize:]
		return true
	}

	inBytes := b.frameSize + adjustSamples*2
	if inBytes < 0 {
		inBytes = 0
	}
	if len(b.buf) < inBytes || inBytes == 0 {
		for i := range dst {
			dst[i] = 0
		}
		return false
	}

	in := b.buf[:inBytes]
	// Consume now (we already have a slice view).
	b.buf = b.buf[inBytes:]

	// Helpers for selecting low-click insertion/removal points.
	readS16 := func(p []byte, off int) int16 {
		// off must be even and within bounds (caller ensures).
		return int16(uint16(p[off]) | uint16(p[off+1])<<8)
	}
	abs16 := func(v int16) int32 {
		if v < 0 {
			return int32(-v)
		}
		return int32(v)
	}
	// findBestCut chooses a sample-aligned byte offset in [min,max] (inclusive)
	// where removing/inserting a sample will be least audible.
	// Metric prefers zero-crossing and low energy around the boundary.
	findBestCut := func(p []byte, minOff, maxOff int) int {
		// Clamp and align.
		if minOff < 2 {
			minOff = 2
		}
		if maxOff > len(p)-4 {
			maxOff = len(p) - 4
		}
		minOff = (minOff / 2) * 2
		maxOff = (maxOff / 2) * 2
		if maxOff < minOff {
			return (len(p) / 2) * 2
		}

		bestOff := minOff
		var bestScore int32 = 1<<31 - 1
		for off := minOff; off <= maxOff; off += 2 {
			a := readS16(p, off-2)
			bb := readS16(p, off)
			c := readS16(p, off+2)

			// Energy around boundary (prefer near-silence).
			e := abs16(bb) + abs16(a) + abs16(c)
			// Discontinuity (prefer minimal jump).
			d := abs16(bb-a) + abs16(c-bb)

			// Bonus for zero-crossing around bb (sign change).
			z := int32(0)
			if (a^bb) < 0 || (bb^c) < 0 {
				z = -2000
			}

			score := e + d + z
			if score < bestScore {
				bestScore = score
				bestOff = off
			}
		}
		return bestOff
	}

	switch adjustSamples {
	case 0:
		copy(dst, in[:b.frameSize])
		return true
	case 1:
		// Drop one sample (2 bytes) from the middle to time-compress slightly.
		// in has frameSize+2 bytes, dst has frameSize bytes.
		mid := b.frameSize / 2
		win := 80 // bytes (~40 samples) search window
		dropAt := findBestCut(in, mid-win, mid+win)
		// Ensure dropAt is within the output span.
		if dropAt < 0 {
			dropAt = 0
		}
		if dropAt > b.frameSize {
			dropAt = b.frameSize
		}
		copy(dst[:dropAt], in[:dropAt])
		copy(dst[dropAt:], in[dropAt+2:])
		return true
	case -1:
		// Duplicate one sample from the middle to time-expand slightly.
		// in has frameSize-2 bytes, dst has frameSize bytes.
		mid := b.frameSize / 2
		win := 80 // bytes (~40 samples)
		dupAt := findBestCut(in, mid-win, mid+win)
		dupAt = (dupAt / 2) * 2
		if dupAt < 2 {
			dupAt = 2
		}
		if dupAt > b.frameSize-2 {
			dupAt = b.frameSize - 2
		}
		// Insert an interpolated sample between the neighbors to minimize clicks.
		// Map: in is shorter by 2 bytes, so in[dupAt:] corresponds to dst[dupAt+2:].
		leftOff := dupAt - 2
		rightOff := dupAt
		if rightOff > len(in)-2 {
			rightOff = len(in) - 2
		}
		l := readS16(in, leftOff)
		rv := readS16(in, rightOff)
		ins := int16((int32(l) + int32(rv)) / 2)
		copy(dst[:dupAt], in[:dupAt])
		dst[dupAt] = byte(uint16(ins))
		dst[dupAt+1] = byte(uint16(ins) >> 8)
		copy(dst[dupAt+2:], in[dupAt:])
		return true
	default:
		copy(dst, in[:b.frameSize])
		return true
	}
}
