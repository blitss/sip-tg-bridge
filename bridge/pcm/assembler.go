package pcm

import "sync"

type FrameAssembler struct {
	frameSize int
	buffer    []byte
	mu        sync.Mutex
}

func NewFrameAssembler(frameSize int) *FrameAssembler {
	return &FrameAssembler{
		frameSize: frameSize,
	}
}

func (a *FrameAssembler) Push(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	a.buffer = append(a.buffer, data...)
	var frames [][]byte
	for len(a.buffer) >= a.frameSize {
		frame := make([]byte, a.frameSize)
		copy(frame, a.buffer[:a.frameSize])
		frames = append(frames, frame)
		a.buffer = a.buffer[a.frameSize:]
	}
	return frames
}
