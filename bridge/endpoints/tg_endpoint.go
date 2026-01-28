package endpoints

import (
	"log/slog"
	"sync"
	"time"

	"gotgcalls/third_party/ntgcalls"
	"gotgcalls/third_party/ubot"

	"gotgcalls/bridge/pcm"
)

type TgEndpoint struct {
	ctx        *ubot.Context
	chatID     int64
	frameSize  int
	sampleRate int
	stepMs     int64
	frames     chan []byte
	done       chan struct{}
	assembler  *pcm.FrameAssembler
	closeOnce  sync.Once
	onClose    func(chatID int64)

	// External microphone timestamps:
	// Telegram expects a stable, monotonic capture timeline in 10ms steps.
	// If we derive timestamps purely from "frames successfully sent", any scheduler/GC
	// pause makes us fall behind real time and can lead to remote-side drop.
	micOnce        sync.Once
	micStart       time.Time
	micStartWallMs int64
	micLastTsMs    int64
}

func NewTgEndpoint(ctx *ubot.Context, chatID int64, frameSize int, sampleRate int, onClose func(chatID int64)) *TgEndpoint {
	// Derive frame step from PCM byte size.
	// PCM16LE mono => 2 bytes/sample.
	stepMs := int64(10)
	if sampleRate > 0 && frameSize > 0 {
		samples := frameSize / 2
		if samples > 0 {
			stepMs = int64(samples*1000) / int64(sampleRate)
			if stepMs < 1 {
				stepMs = 1
			}
		}
	}

	return &TgEndpoint{
		ctx:        ctx,
		chatID:     chatID,
		frameSize:  frameSize,
		sampleRate: sampleRate,
		stepMs:     stepMs,
		frames:     make(chan []byte, 20),
		done:       make(chan struct{}),
		assembler:  pcm.NewFrameAssembler(frameSize),
		onClose:    onClose,
	}
}

func (s *TgEndpoint) ChatID() int64 {
	return s.chatID
}

func (s *TgEndpoint) SpeakerFrames() <-chan []byte {
	return s.frames
}

func (s *TgEndpoint) Done() <-chan struct{} {
	return s.done
}

func (s *TgEndpoint) Format() pcm.AudioFormat {
	return pcm.AudioFormat{
		SampleRate: s.sampleRate,
		Channels:   1,
		FrameDur:   time.Duration(s.stepMs) * time.Millisecond,
	}
}

func (s *TgEndpoint) PushSpeakerFrames(frames []ntgcalls.Frame) {
	for _, frame := range frames {
		for _, normalized := range s.assembler.Push(frame.Data) {
			select {
			case <-s.done:
				return
			case s.frames <- normalized:
			}
		}
	}
}

var sendFrameLogCount int64

func (s *TgEndpoint) SendPCMFrame10ms(pcmFrame []byte) error {
	step := s.stepMs
	if step < 1 {
		step = 10
	}
	s.micOnce.Do(func() {
		t := time.Now() // contains monotonic clock reading
		s.micStart = t
		s.micStartWallMs = t.UnixMilli()
		s.micLastTsMs = s.micStartWallMs - step
	})

	// Quantize monotonic elapsed time to our frame step.
	elapsedMs := time.Since(s.micStart).Milliseconds()
	ts := s.micStartWallMs + (elapsedMs/step)*step

	// Never go backwards / same timestamp.
	if ts <= s.micLastTsMs {
		ts = s.micLastTsMs + step
	}
	s.micLastTsMs = ts

	frameData := ntgcalls.FrameData{AbsoluteCaptureTimestampMs: ts}
	err := s.ctx.SendExternalFrame(s.chatID, ntgcalls.MicrophoneStream, pcmFrame, frameData)
	sendFrameLogCount++
	if sendFrameLogCount <= 5 || (sendFrameLogCount <= 200 && sendFrameLogCount%50 == 0) {
		if err != nil {
			slog.Warn("tg send frame failed", "count", sendFrameLogCount, "size", len(pcmFrame), "ts_ms", ts, "error", err)
		} else {
			slog.Info("tg send frame ok", "count", sendFrameLogCount, "size", len(pcmFrame), "ts_ms", ts)
		}
	}
	return err
}

func (s *TgEndpoint) Close() {
	s.closeOnce.Do(func() {
		_ = s.ctx.Stop(s.chatID)
		close(s.done)
		if s.onClose != nil {
			s.onClose(s.chatID)
		}
	})
}
