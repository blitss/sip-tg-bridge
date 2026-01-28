package bridge

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/emiago/diago/media"
	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/protocol/logger"
	"github.com/pion/rtp"

	"gotgcalls/bridge/endpoints"
	"gotgcalls/bridge/pcm"
	"gotgcalls/bridge/pipeline"
)

type MediaBridge struct {
	ctx           context.Context
	cancel        context.CancelFunc
	logger        *slog.Logger
	sipFormat     pcm.AudioFormat
	tgFormat      pcm.AudioFormat
	sip           *endpoints.SipEndpoint
	tg            *endpoints.TgEndpoint
	sipToTGBuffer *pcm.PCMPlayoutBuffer
	driftTarget   int
	driftMaxBurst int
	wg            sync.WaitGroup

	// driftAcc accumulates how many 1-sample adjustments we should apply.
	// Positive => consume extra samples (shrink backlog), negative => consume fewer (grow backlog).
	driftAcc int
}

func NewMediaBridge(parent context.Context, logger *slog.Logger, sip *endpoints.SipEndpoint, tg *endpoints.TgEndpoint, driftTarget int, driftMaxBurst int) (*MediaBridge, error) {
	ctx, cancel := context.WithCancel(parent)
	if logger == nil {
		logger = slog.Default()
	}
	// NOTE: With media-sdk pipeline, decode/encode paths do their own resampling
	// via msdk.ResampleWriter, so we don't need explicit resamplers here.
	if driftTarget < 1 {
		driftTarget = 1
	}
	if driftMaxBurst < 1 {
		driftMaxBurst = 1
	}
	sipFormat := sip.Format()
	tgFormat := tg.Format()
	return &MediaBridge{
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
		sipFormat: sipFormat,
		tgFormat:  tgFormat,
		sip:       sip,
		tg:        tg,
		// PCM playout buffer decouples bursty SIP decode from TG real-time pacing.
		sipToTGBuffer: pcm.NewPCMPlayoutBuffer(tgFormat.FrameBytes()),
		driftTarget:   driftTarget,
		driftMaxBurst: driftMaxBurst,
	}, nil
}

func (b *MediaBridge) Start() {
	b.logger.Info("media bridge starting",
		"sip_rate", b.sipFormat.SampleRate,
		"tg_rate", b.tgFormat.SampleRate,
		"sip_frame_size", b.sipFormat.FrameBytes(),
		"tg_frame_size", b.tgFormat.FrameBytes(),
	)
	b.wg.Add(3)
	go b.readSIP()
	go b.writeTG()
	go b.writeSIP()
}

func (b *MediaBridge) Stop() {
	b.logger.Info("media bridge stopping")
	b.cancel()
	b.wg.Wait()
	b.logger.Info("media bridge stopped")
}

func (b *MediaBridge) readSIP() {
	defer b.wg.Done()
	if b.sip == nil || b.sip.LKCodec == nil {
		b.logger.Warn("sip media not ready (no codec)")
		return
	}
	if b.sip.RTPReader() == nil {
		b.logger.Warn("sip rtp reader not available")
		return
	}

	// Build LiveKit-like pipeline: jitter -> silence filler -> codec decode -> TG playout buffer.
	pt := b.sip.PayloadType()
	hc, err := pipeline.BuildSipDecodeChain(pipeline.SipDecodeConfig{
		Codec:         b.sip.LKCodec,
		PayloadType:   pt,
		InputChannels: b.sip.Channels,
		OutputFormat:  b.tgFormat,
		PlayoutBuffer: b.sipToTGBuffer,
		EnableJitter:  b.sip.EnableJitter,
		Log:           logger.GetLogger(),
	})
	if err != nil {
		b.logger.Warn("sip decode chain failed", "error", err)
		return
	}
	defer hc.Close()

	rtpBuf := make([]byte, media.RTPBufSize)
	pkt := &rtp.Packet{}
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		*pkt = rtp.Packet{}
		_, err := b.sip.RTPReader().ReadRTP(rtpBuf, pkt)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				b.logger.Warn("sip rtp read failed", "error", err)
			}
			return
		}

		// Filter only negotiated payload type.
		if uint8(pkt.PayloadType) != pt || len(pkt.Payload) == 0 {
			continue
		}

		// IMPORTANT: jitter buffer keeps payload references; clone to avoid reuse bugs.
		payload := append([]byte(nil), pkt.Payload...)
		if err := hc.HandleRTP(&pkt.Header, payload); err != nil {
			b.logger.Warn("sip rtp handler failed", "error", err)
			return
		}
	}
}

func (b *MediaBridge) writeTG() {
	defer b.wg.Done()
	// TG external mic injection is done in 10ms steps.
	tgFrameDur := b.tgFormat.FrameDur
	b.logger.Info("writeTG goroutine started", "tg_frame_dur_ms", tgFrameDur.Milliseconds())
	ticker := time.NewTicker(tgFrameDur)
	defer ticker.Stop()
	frameBuf := make([]byte, b.tgFormat.FrameBytes())
	frameCount := 0
	realFrameCount := 0
	lastRealAt := time.Now()
	lastStatsAt := time.Now()
	lastUnderflowAt := time.Time{}
	var lastEnergy float64
	var adjPos, adjNeg uint64
	for {
		select {
		case <-b.ctx.Done():
			b.logger.Info("writeTG stopped", "frames_sent", frameCount, "real_frames", realFrameCount)
			return
		case <-ticker.C:
			backlog := b.sipToTGBuffer.LenFrames()
			// Drift control (LiveKit-like idea): avoid dropping whole frames.
			// Instead, apply tiny time-compression/expansion by +/-1 PCM16 sample
			// within an output frame to nudge backlog toward driftTarget.
			//
			// We still keep an emergency hard cap to avoid unbounded latency if
			// something goes very wrong.
			if backlog > b.driftTarget+200 {
				dropped := b.sipToTGBuffer.DropFrames(backlog - b.driftTarget)
				if dropped > 0 {
					b.logger.Warn("sip->tg emergency drop (hard cap)", "dropped_frames", dropped, "backlog_before", backlog, "target", b.driftTarget)
				}
				b.driftAcc = 0
				backlog = b.sipToTGBuffer.LenFrames()
			}

			// Accumulate error with hysteresis so we don't flap.
			errFrames := backlog - b.driftTarget
			if errFrames >= 2 {
				b.driftAcc += errFrames / 2
			} else if errFrames <= -2 {
				b.driftAcc += errFrames / 2 // negative
			}

			adjust := 0
			if b.driftAcc > 0 {
				adjust = 1
				b.driftAcc--
				adjPos++
			} else if b.driftAcc < 0 {
				adjust = -1
				b.driftAcc++
				adjNeg++
			}

			ok := b.sipToTGBuffer.ReadIntoAdjust(frameBuf, adjust)
			frameCount++
			if ok {
				realFrameCount++
				lastRealAt = time.Now()
				lastEnergy = pcm16leMonoEnergy(frameBuf)
			}
			// Emit periodic stats so we can see if TG "goes silent" because:
			// - we are underflowing (queue empty -> fallback silence), or
			// - upstream audio frames are effectively zero-energy.
			if time.Since(lastStatsAt) >= 5*time.Second {
				b.logger.Info("sip->tg stats",
					"frames_sent", frameCount,
					"real_frames", realFrameCount,
					"queue_len", b.sipToTGBuffer.LenFrames(),
					"drift_acc", b.driftAcc,
					"adj_pos", adjPos,
					"adj_neg", adjNeg,
					"ms_since_last_real", time.Since(lastRealAt).Milliseconds(),
					"last_energy", lastEnergy,
				)
				lastStatsAt = time.Now()
			}
			// Warn if we haven't seen non-fallback frames in a while.
			// Rate-limit to avoid log spam during long underflows.
			if time.Since(lastRealAt) >= 2*time.Second && time.Since(lastUnderflowAt) >= 2*time.Second {
				b.logger.Warn("sip->tg underflow (sending silence)",
					"ms_since_last_real", time.Since(lastRealAt).Milliseconds(),
					"queue_len", b.sipToTGBuffer.LenFrames(),
				)
				lastUnderflowAt = time.Now()
			}
			if frameCount == 1 {
				b.logger.Info("sip->tg sending started", "frame_size", len(frameBuf), "expected_size", b.tgFormat.FrameBytes(), "is_silence", !ok, "queue_len", b.sipToTGBuffer.LenFrames())
			}
			if realFrameCount == 1 && ok {
				b.logger.Info("sip->tg first real frame!", "total_sent", frameCount)
			}
			if err := b.tg.SendPCMFrame10ms(frameBuf); err != nil {
				b.logger.Warn("tg mic send failed", "error", err)
				return
			}
		}
	}
}

// pcm16leMonoEnergy computes a simple RMS-like energy metric for PCM16 LE mono.
// Returns 0 for silence, higher values for louder audio.
func pcm16leMonoEnergy(pcm []byte) float64 {
	if len(pcm) < 2 {
		return 0
	}
	var sum float64
	samples := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		f := float64(v) / 32768.0
		sum += f * f
		samples++
	}
	if samples == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(samples))
}

func (b *MediaBridge) writeSIP() {
	defer b.wg.Done()
	if b.sip == nil || b.sip.LKCodec == nil {
		b.logger.Warn("sip media not ready (no codec)")
		return
	}
	if b.sip.RTPWriter() == nil {
		b.logger.Warn("sip rtp writer not available")
		return
	}

	// media-sdk assumes 20ms frames in its RTP stream timestamping.
	// We keep TG pacing at 10ms, but only encode/send every 20ms (two TG frames).
	tgFrameDur := b.tgFormat.FrameDur
	ticker := time.NewTicker(tgFrameDur)
	defer ticker.Stop()
	silence := make([]byte, b.tgFormat.FrameBytes())

	pt := b.sip.PayloadType()
	lkInfo := b.sip.LKCodec.Info()
	enc, err := pipeline.BuildSipEncodePipeline(pipeline.SipEncodeConfig{
		Codec:       b.sip.LKCodec,
		PayloadType: pt,
		RTPClock:    b.sip.RTPClockRate,
		SourceRate:  b.tgFormat.SampleRate,
		RTPWriter:   b.sip.RTPWriter(),
	})
	if err != nil {
		b.logger.Warn("sip encode pipeline failed", "error", err)
		return
	}
	out := enc.Writer

	// Assemble TG 10ms frames into 20ms PCM16 samples at TG rate.
	tgSamplesPer10ms := b.tgFormat.FrameBytes() / 2 // interleaved samples
	assembler := pcm.NewPCM16Assembler(tgSamplesPer10ms * 2)

	var (
		tgFrameCount   int
		sipFrameCount  int
		realFrameCount int

		inBuf     msdk.PCM16Sample
		tmpCh     msdk.PCM16Sample
		lastWrite time.Time
	)
	for {
		select {
		case <-b.ctx.Done():
			b.logger.Info("writeSIP stopped", "tg_frames", tgFrameCount, "sip_frames", sipFrameCount, "real_frames", realFrameCount)
			return
		case <-ticker.C:
			backlog := len(b.tg.SpeakerFrames())
			// Keep real-time pace; drop oldest frames if TG backlog grows.
			if backlog > b.driftTarget {
				// Drop gradually to avoid audible "time jumps".
				toDrop := backlog - b.driftTarget
				if b.driftMaxBurst > 0 && toDrop > b.driftMaxBurst {
					toDrop = b.driftMaxBurst
				}
				dropped := drainFrames(b.tg.SpeakerFrames(), toDrop)
				if dropped > 0 && (dropped >= 10 || tgFrameCount == 0) {
					b.logger.Warn("tg->sip backlog drop", "dropped_frames", dropped, "backlog_before", backlog, "target", b.driftTarget)
				}
			}

			frame := popFrame(b.tg.SpeakerFrames(), silence)
			tgFrameCount++
			isSilence := &frame[0] == &silence[0]
			if !isSilence {
				realFrameCount++
			}

			// bytes -> PCM16Sample (TG sample rate)
			inBuf = pcm.PCM16BytesToSample(inBuf, frame)

			for _, outFrame := range assembler.Push(inBuf) {
				sipFrameCount++

				// If we are delayed vs wall clock, advance RTP timestamp to avoid "playing in the past".
				if !lastWrite.IsZero() {
					dt := time.Since(lastWrite)
					if dt > b.sipFormat.FrameDur*2 {
						skip := dt - b.sipFormat.FrameDur
						if skip > 0 {
							enc.Delay(uint32(skip.Seconds() * float64(lkInfo.RTPClockRate)))
						}
					}
				}

				// Channel conversion (TG mono <-> SIP stereo) at TG rate, before resample+encode.
				tmpCh = pcm.PCM16ConvertChannels(tmpCh, outFrame, 1, b.sip.Channels)

				if err := out.WriteSample(tmpCh); err != nil {
					b.logger.Warn("sip rtp encode/write failed", "error", err)
					return
				}
				lastWrite = time.Now()
			}
		}
	}
}

func drainFrames(queue <-chan []byte, max int) int {
	dropped := 0
	for dropped < max {
		select {
		case <-queue:
			dropped++
		default:
			return dropped
		}
	}
	return dropped
}

func popFrame(queue <-chan []byte, fallback []byte) []byte {
	select {
	case frame := <-queue:
		return frame
	default:
		return fallback
	}
}
