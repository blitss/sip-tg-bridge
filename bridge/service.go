package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gotgcalls/third_party/ntgcalls"
	"gotgcalls/third_party/ubot"

	"github.com/Laky-64/gologging"
	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
	"github.com/emiago/diago/media/sdp"
	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	msdk "github.com/livekit/media-sdk"

	"gotgcalls/bridge/endpoints"
	"gotgcalls/bridge/pcm"
)

type Service struct {
	cfg         Config
	sip         *diago.Diago
	tg          *ubot.Context
	logger      *slog.Logger
	mu          sync.Mutex
	tgSessions  map[int64]*endpoints.TgEndpoint
	activeCalls atomic.Int64
	authServer  *diago.DigestAuthServer
}

func NewService(cfg Config, sip *diago.Diago, tg *ubot.Context, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	gologging.SetLevel(gologging.FatalLevel)
	gologging.GetLogger("ntgcalls").SetLevel(gologging.FatalLevel)

	var authServer *diago.DigestAuthServer
	if cfg.SIPAuthUser != "" && cfg.SIPAuthPass != "" {
		authServer = diago.NewDigestServer()
	}
	return &Service{
		cfg:        cfg,
		sip:        sip,
		tg:         tg,
		logger:     logger,
		tgSessions: map[int64]*endpoints.TgEndpoint{},
		authServer: authServer,
	}
}

func (s *Service) Start(ctx context.Context) error {
	s.tg.OnIncomingCall(func(_ *ubot.Context, chatID int64) {
		go s.handleIncomingTG(ctx, chatID)
	})
	s.tg.OnFrame(s.handleTGFrame)
	s.tg.OnStreamEnd(s.handleTGStreamEnd)
	s.tg.OnCallDisconnect(s.handleTGCallDisconnect)

	return s.sip.Serve(ctx, func(inDialog *diago.DialogServerSession) {
		s.handleIncomingSIP(inDialog)
	})
}

func (s *Service) handleIncomingSIP(inDialog *diago.DialogServerSession) {
	callStart := time.Now()
	callLogger := s.logger.With(
		"call_id", sipCallID(inDialog),
		"sip_from", inDialog.FromUser(),
		"sip_to", inDialog.ToUser(),
	)
	callLogger.Info("sip: handler started", "time_ns", callStart.UnixNano())

	// Check if dialog context is already done
	select {
	case <-inDialog.Context().Done():
		callLogger.Error("sip: dialog context ALREADY CANCELED on entry!", "error", inDialog.Context().Err())
	default:
		callLogger.Info("sip: dialog context is active")
	}

	callLogger.Info("sip: incoming call received",
		"via", inDialog.InviteRequest.Via().Value(),
		"contact", inDialog.InviteRequest.Contact().Value(),
	)

	if err := s.authorizeInboundSIP(inDialog, callLogger); err != nil {
		callLogger.Info("sip: call rejected (auth failed)")
		return
	}
	if !s.allowCall(callLogger) {
		callLogger.Info("sip: call rejected (busy)")
		_ = inDialog.Respond(sip.StatusBusyHere, "Busy", nil)
		return
	}
	defer s.activeCalls.Add(-1)
	defer inDialog.Close()

	// Monitor SIP caller hangup during setup
	sipHangupCh := make(chan struct{})
	go func() {
		<-inDialog.Context().Done()
		close(sipHangupCh)
		callLogger.Info("sip: caller context done (hangup or cancel)", "reason", inDialog.Context().Err())
	}()

	chatID := s.cfg.TGUserID

	callLogger.Info("sip: sending trying")
	if err := inDialog.Trying(); err != nil {
		callLogger.Error("sip trying failed", "error", err)
	} else {
		callLogger.Info("sip: trying sent ok")
	}
	callLogger.Info("sip: sending ringing")
	if err := inDialog.Ringing(); err != nil {
		callLogger.Error("sip ringing failed", "error", err)
	} else {
		callLogger.Info("sip: ringing sent ok")
	}

	callCtx, cancel := context.WithTimeout(inDialog.Context(), s.cfg.EstablishTimeout)
	defer cancel()

	if err := s.validateSDPPolicy(inDialog.InviteRequest.Body()); err != nil {
		callLogger.Warn("sip sdp policy rejected", "error", err)
		_ = inDialog.Respond(sip.StatusNotAcceptableHere, "Unsupported SDP", nil)
		return
	}
	logSDPAudioCodecs(callLogger, "remote offer", inDialog.InviteRequest.Body())

	callLogger.Info("sip: starting telegram call setup")
	tgSession, err := s.startTGCall(callCtx, chatID)
	if err != nil {
		// Check if caller hung up during TG setup
		select {
		case <-sipHangupCh:
			callLogger.Warn("tg setup aborted: sip caller hung up during setup", "chat_id", chatID, "error", err)
		default:
			callLogger.Warn("tg setup failed", "chat_id", chatID, "error", err)
		}
		callLogger.Warn("sip: SENDING 480 NOW")
		_ = inDialog.Respond(sip.StatusTemporarilyUnavailable, "Telegram unavailable", nil)
		return
	}
	defer tgSession.Close()
	callLogger.Info("sip: telegram call ready")

	localPrefs := s.sipCodecs()
	logCodecPrefs(callLogger, "local codec preferences", localPrefs)

	if s.cfg.EnableEarlyMedia {
		callLogger.Info("sip: sending early media (183)")
		if err := inDialog.ProgressMediaOptions(diago.ProgressMediaOptions{Codecs: localPrefs}); err != nil {
			callLogger.Warn("sip early media failed", "error", err)
			return
		}
	}

	callLogger.Info("sip: answering call (200 OK)")
	if err := inDialog.AnswerOptions(diago.AnswerOptions{Codecs: localPrefs}); err != nil {
		callLogger.Warn("sip answer failed", "error", err)
		return
	}
	callLogger.Info("sip: call answered, setting up media")

	sipMedia, err := endpoints.NewSipEndpoint(inDialog, endpoints.SIPMediaConfig{
		JitterMinPackets: s.cfg.JitterMinPackets,
		FrameDuration:    s.cfg.FrameDuration,
	})
	if err != nil {
		callLogger.Warn("sip media setup failed", "error", err)
		return
	}
	defer sipMedia.Close()
	callLogger.Info("sip: codec negotiated",
		"codec", sipMedia.Codec.Name,
		"payload_type", sipMedia.Codec.PayloadType,
		"pcm_rate", sipMedia.SampleRate,
		"rtp_clock_rate", sipMedia.RTPClockRate,
	)

	if s.cfg.EnableDTMF {
		s.startDTMFListener(inDialog.Context(), inDialog.Media(), callLogger)
	}

	bridge, err := NewMediaBridge(
		inDialog.Context(),
		callLogger,
		sipMedia,
		tgSession,
		s.cfg.DriftTargetFrames,
		s.cfg.DriftMaxBurst,
	)
	if err != nil {
		callLogger.Warn("bridge init failed", "error", err)
		return
	}
	bridge.Start()
	defer bridge.Stop()

	callLogger.Info("sip: call in progress (media bridged)")

	select {
	case <-inDialog.Context().Done():
		callLogger.Info("sip: call ended - caller hung up", "duration", time.Since(callStart).Round(time.Millisecond))
	case <-tgSession.Done():
		callLogger.Info("sip: call ended - telegram side ended", "duration", time.Since(callStart).Round(time.Millisecond))
	}
}

func (s *Service) handleIncomingTG(ctx context.Context, chatID int64) {
	callLogger := s.logger.With("tg_chat_id", chatID)
	if chatID != s.cfg.TGUserID {
		callLogger.Warn("tg call rejected (unexpected user)")
		_ = s.tg.Stop(chatID)
		return
	}
	callLogger.Warn("tg call rejected (use /call command)")
	_ = s.tg.Stop(chatID)
}

func (s *Service) StartCallFromCommand(ctx context.Context, number string) error {
	chatID := s.cfg.TGUserID
	callLogger := s.logger.With("tg_chat_id", chatID, "dial", number)
	if !s.allowCall(callLogger) {
		return errors.New("active call limit reached")
	}
	defer s.activeCalls.Add(-1)

	callCtx, cancel := context.WithTimeout(ctx, s.cfg.EstablishTimeout)
	defer cancel()

	tgSession, err := s.startTGCall(callCtx, chatID)
	if err != nil {
		callLogger.Warn("tg setup failed", "chat_id", chatID, "error", err)
		return err
	}
	defer tgSession.Close()

	recipient, err := s.buildOutboundURI(number)
	if err != nil {
		callLogger.Warn("invalid sip target", "number", number, "error", err)
		return err
	}

	dialog, earlyMedia, err := s.inviteWithEarlyMedia(callCtx, recipient, callLogger)
	if err != nil {
		callLogger.Warn("sip invite failed", "error", err)
		return err
	}
	defer dialog.Close()

	callLogger = callLogger.With("call_id", sipCallID(dialog))
	sipMedia, err := endpoints.NewSipEndpoint(dialog, endpoints.SIPMediaConfig{
		JitterMinPackets: s.cfg.JitterMinPackets,
		FrameDuration:    s.cfg.FrameDuration,
	})
	if err != nil {
		callLogger.Warn("sip media setup failed", "error", err)
		return err
	}
	defer sipMedia.Close()
	callLogger.Info("sip: codec negotiated",
		"codec", sipMedia.Codec.Name,
		"payload_type", sipMedia.Codec.PayloadType,
		"pcm_rate", sipMedia.SampleRate,
		"rtp_clock_rate", sipMedia.RTPClockRate,
	)

	if s.cfg.EnableDTMF {
		s.startDTMFListener(dialog.Context(), dialog.Media(), callLogger)
	}

	bridge, err := NewMediaBridge(
		dialog.Context(),
		callLogger,
		sipMedia,
		tgSession,
		s.cfg.DriftTargetFrames,
		s.cfg.DriftMaxBurst,
	)
	if err != nil {
		callLogger.Warn("bridge init failed", "error", err)
		return err
	}
	bridge.Start()
	defer bridge.Stop()

	if earlyMedia {
		if err := dialog.WaitAnswer(callCtx, sipgo.AnswerOptions{}); err != nil {
			callLogger.Warn("sip wait answer failed", "error", err)
			return err
		}
		if err := dialog.Ack(callCtx); err != nil {
			callLogger.Warn("sip ack failed", "error", err)
			return err
		}
	}

	select {
	case <-dialog.Context().Done():
	case <-tgSession.Done():
	}
	return nil
}

var tgFrameLogCount int64

func (s *Service) handleTGFrame(chatID int64, mode ntgcalls.StreamMode, device ntgcalls.StreamDevice, frames []ntgcalls.Frame) {
	tgFrameLogCount++
	if tgFrameLogCount <= 5 {
		totalBytes := 0
		for _, f := range frames {
			totalBytes += len(f.Data)
		}
		s.logger.Info("tg frame received", "chat_id", chatID, "mode", mode, "device", device, "frame_count", len(frames), "total_bytes", totalBytes)
	}
	// Accept frames from PlaybackStream (receiving from TG)
	if mode != ntgcalls.PlaybackStream {
		return
	}
	session := s.getTGSession(chatID)
	if session == nil {
		return
	}
	session.PushSpeakerFrames(frames)
}

func (s *Service) handleTGStreamEnd(chatID int64, streamType ntgcalls.StreamType, _ ntgcalls.StreamDevice) {
	if streamType != ntgcalls.AudioStream {
		return
	}
	session := s.getTGSession(chatID)
	if session != nil {
		session.Close()
	}
}

func (s *Service) handleTGCallDisconnect(chatID int64, reason string) {
	s.logger.Info("tg call disconnected", "chat_id", chatID, "reason", reason)
	session := s.getTGSession(chatID)
	if session != nil {
		session.Close()
	}
}

func (s *Service) startTGCall(ctx context.Context, chatID int64) (*endpoints.TgEndpoint, error) {
	session := s.ensureTGSession(chatID)

	capture := ntgcalls.MediaDescription{
		Microphone: &ntgcalls.AudioDescription{
			MediaSource:  ntgcalls.MediaSourceExternal,
			SampleRate:   uint32(s.cfg.SampleRate),
			ChannelCount: uint8(s.cfg.Channels),
			KeepOpen:     true,
		},
	}
	playback := ntgcalls.MediaDescription{
		Microphone: &ntgcalls.AudioDescription{
			MediaSource:  ntgcalls.MediaSourceExternal,
			SampleRate:   uint32(s.cfg.SampleRate),
			ChannelCount: uint8(s.cfg.Channels),
			KeepOpen:     true,
		},
	}
	s.logger.Info("tg call: initiating play stream", "chat_id", chatID)
	if err := s.tg.Play(chatID, capture); err != nil {
		s.logger.Error("tg play failed", "chat_id", chatID, "error", err, "error_type", fmt.Sprintf("%T", err))
		session.Close()
		return nil, fmt.Errorf("tg play: %w", err)
	}
	s.logger.Info("tg call: play stream ready, initiating record stream", "chat_id", chatID)
	if err := s.tg.Record(chatID, playback); err != nil {
		session.Close()
		return nil, fmt.Errorf("tg record: %w", err)
	}
	s.logger.Info("tg call: connected and ready", "chat_id", chatID)

	// Note: We don't check ctx.Done() here anymore because the TG session
	// is already established. If the SIP side canceled during setup, we still
	// want to return the session and let the caller handle cleanup properly.
	// The caller will detect the canceled context and clean up appropriately.

	return session, nil
}

func (s *Service) ensureTGSession(chatID int64) *endpoints.TgEndpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.tgSessions[chatID]; ok {
		return session
	}
	frameSize := s.frameSize()
	session := endpoints.NewTgEndpoint(s.tg, chatID, frameSize, s.cfg.SampleRate, s.removeTGSession)
	s.tgSessions[chatID] = session
	return session
}

func (s *Service) getTGSession(chatID int64) *endpoints.TgEndpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tgSessions[chatID]
}

func (s *Service) removeTGSession(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tgSessions, chatID)
}

func (s *Service) buildOutboundURI(number string) (sip.Uri, error) {
	normalized := normalizePhone(number)
	if normalized == "" {
		return sip.Uri{}, fmt.Errorf("invalid phone number")
	}
	host, port := splitHostPort(s.cfg.SIPProvider)
	recipient := sip.Uri{
		User: normalized,
		Host: host,
	}
	if port > 0 {
		recipient.Port = port
	}
	if s.cfg.SIPTransport != "" {
		recipient.UriParams = sip.HeaderParams{"transport": s.cfg.SIPTransport}
	}
	return recipient, nil
}

func (s *Service) sipCodecs() []media.Codec {
	return SIPCodecs(s.cfg)
}

func (s *Service) frameSize() int {
	// TG external audio injection is most stable with 10ms PCM blocks.
	// We keep SIP ptime (FrameDuration) at e.g. 20ms, but we inject into TG at half that.
	tgFrameDuration := s.cfg.FrameDuration / 2
	format := pcm.AudioFormat{
		SampleRate: s.cfg.SampleRate,
		Channels:   s.cfg.Channels,
		FrameDur:   tgFrameDuration,
	}
	return format.FrameBytes()
}

func (s *Service) allowCall(logger *slog.Logger) bool {
	if s.cfg.MaxActiveCalls <= 0 {
		s.activeCalls.Add(1)
		return true
	}
	for {
		current := s.activeCalls.Load()
		if current >= s.cfg.MaxActiveCalls {
			logger.Warn("active call limit reached", "max", s.cfg.MaxActiveCalls)
			return false
		}
		if s.activeCalls.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func sipCallID(dialog diago.DialogSession) string {
	if dialog == nil {
		return ""
	}
	req := dialog.DialogSIP().InviteRequest
	if req == nil || req.CallID() == nil {
		return ""
	}
	return req.CallID().Value()
}

func (s *Service) inviteWithEarlyMedia(ctx context.Context, recipient sip.Uri, logger *slog.Logger) (*diago.DialogClientSession, bool, error) {
	dialog, err := s.sip.NewDialog(recipient, diago.NewDialogOptions{})
	if err != nil {
		return nil, false, err
	}
	headers := []sip.Header{}
	if logger != nil {
		if ms := dialog.MediaSession(); ms != nil {
			logCodecPrefs(logger, "local codec offer (outbound INVITE)", ms.Codecs)
		}
	}
	err = dialog.Invite(ctx, diago.InviteClientOptions{
		EarlyMediaDetect: s.cfg.EnableEarlyMedia,
		Username:         s.cfg.SIPAuthUser,
		Password:         s.cfg.SIPAuthPass,
		OnResponse: func(res *sip.Response) error {
			if res.ContentType() != nil && res.ContentType().Value() == "application/sdp" {
				if logger != nil {
					logSDPAudioCodecs(logger, "remote answer", res.Body())
				}
				return s.validateSDPPolicy(res.Body())
			}
			return nil
		},
		Headers: headers,
	})
	if err != nil {
		if errors.Is(err, diago.ErrClientEarlyMedia) {
			return dialog, true, nil
		}
		_ = dialog.Close()
		return nil, false, err
	}
	if err := dialog.Ack(ctx); err != nil {
		_ = dialog.Close()
		return nil, false, err
	}
	return dialog, false, nil
}

func (s *Service) validateSDPPolicy(body []byte) error {
	if body == nil {
		return errors.New("missing SDP")
	}
	expectedPtime := int(s.cfg.FrameDuration / time.Millisecond)
	desc := sdp.SessionDescription{}
	if err := sdp.Unmarshal(body, &desc); err != nil {
		return err
	}
	attrs := desc.Values("a")
	ptime, hasPtime := parseSDPTimeAttr(attrs, "ptime")
	maxptime, hasMaxPtime := parseSDPTimeAttr(attrs, "maxptime")
	if hasPtime && ptime != expectedPtime {
		return errors.New("unsupported ptime")
	}
	if hasMaxPtime && maxptime < expectedPtime {
		return errors.New("unsupported maxptime")
	}
	return nil
}

func parseSDPTimeAttr(attrs []string, key string) (int, bool) {
	prefix := key + ":"
	for _, attr := range attrs {
		if !strings.HasPrefix(attr, prefix) {
			continue
		}
		value := strings.TrimPrefix(attr, prefix)
		ptime, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return ptime, true
		}
	}
	return 0, false
}

func (s *Service) startDTMFListener(ctx context.Context, dialogMedia *diago.DialogMedia, logger *slog.Logger) {
	if dialogMedia == nil {
		return
	}
	dtmfReader := dialogMedia.AudioReaderDTMF()
	if dtmfReader == nil {
		return
	}
	go func() {
		dtmfReader.OnDTMF(func(digit rune) error {
			logger.Info("DTMF received", "digit", string(digit))
			return nil
		})
		<-ctx.Done()
	}()
}

func (s *Service) authorizeInboundSIP(dialog *diago.DialogServerSession, logger *slog.Logger) error {
	if s.authServer == nil {
		return nil
	}
	auth := diago.DigestAuth{
		Username: s.cfg.SIPAuthUser,
		Password: s.cfg.SIPAuthPass,
		Realm:    s.cfg.SIPAuthRealm,
	}
	if err := s.authServer.AuthorizeDialog(dialog, auth); err != nil {
		logger.Warn("sip auth failed", "error", err)
		return err
	}
	return nil
}

func normalizePhone(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		if r == '+' && i == 0 {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || out == "+" {
		return ""
	}
	return out
}

func SIPCodecs(cfg Config) []media.Codec {
	// Map codecs from media-sdk registry -> diago SDP codecs.
	//
	// No hardcoded PT exceptions: payload types are assigned like media-sdk/sdp.OfferCodecs():
	// - static codecs use RTPDefType
	// - dynamic codecs get sequential PTs starting from 101
	//
	// diago needs: Name, PayloadType, SampleRate, SampleDur, NumChannels.

	enabled := msdk.EnabledCodecs()
	slices.SortFunc(enabled, func(a, b msdk.Codec) int {
		ai, bi := a.Info(), b.Info()
		if ai.RTPIsStatic != bi.RTPIsStatic {
			if ai.RTPIsStatic {
				return -1
			}
			return 1
		}
		return bi.Priority - ai.Priority
	})

	usedPT := map[uint8]bool{}
	const dynamicStart = uint8(101)
	nextDynamic := dynamicStart

	allocDynamic := func() uint8 {
		for usedPT[nextDynamic] {
			nextDynamic++
			// We don't expect to exhaust dynamic PT space in SIP audio offers.
			if nextDynamic < 96 {
				nextDynamic = 96
			}
		}
		pt := nextDynamic
		nextDynamic++
		return pt
	}

	codecs := make([]media.Codec, 0, len(enabled)+2)

	for _, c := range enabled {
		info := c.Info()
		sdpName := strings.TrimSpace(info.SDPName)
		if sdpName == "" {
			continue
		}
		// Optional: don't advertise DTMF if disabled.
		if strings.HasPrefix(strings.ToLower(sdpName), "telephone-event/") && !cfg.EnableDTMF {
			continue
		}

		dc, ok := media.CodecFromSDPName(sdpName, 0, cfg.FrameDuration)
		if !ok {
			continue
		}

		pt := uint8(0)
		if info.RTPIsStatic {
			pt = info.RTPDefType
		} else {
			pt = allocDynamic()
		}
		if usedPT[pt] {
			// Shouldn't happen, but stay defensive.
			continue
		}
		usedPT[pt] = true

		dc.PayloadType = pt
		codecs = append(codecs, dc)
	}

	// If DTMF is enabled but media-sdk didn't register it for some reason,
	// still advertise it so diago can negotiate telephone-event.
	if cfg.EnableDTMF {
		hasDTMF := false
		for _, c := range codecs {
			if strings.EqualFold(c.Name, "telephone-event") {
				hasDTMF = true
				break
			}
		}
		if !hasDTMF {
			pt := allocDynamic()
			codecs = append(codecs, media.Codec{
				Name:        "telephone-event",
				PayloadType: pt,
				SampleRate:  8000,
				SampleDur:   cfg.FrameDuration,
				NumChannels: 1,
			})
		}
	}

	if len(codecs) == 0 {
		codecs = append(codecs, media.CodecAudioUlaw(cfg.FrameDuration))
	}
	return codecs
}

func logSDPAudioCodecs(logger *slog.Logger, label string, body []byte) {
	if logger == nil || len(body) == 0 {
		return
	}
	desc := sdp.SessionDescription{}
	if err := sdp.Unmarshal(body, &desc); err != nil {
		logger.Warn("sip: failed to parse sdp", "label", label, "error", err)
		return
	}
	md, err := desc.MediaDescription("audio")
	if err != nil {
		logger.Warn("sip: no audio media in sdp", "label", label, "error", err)
		return
	}

	attrs := desc.Values("a")
	tmp := make([]media.Codec, len(md.Formats))
	n, perr := media.CodecsFromSDPRead(md.Formats, attrs, tmp)
	if n < 0 {
		n = 0
	}
	if n > len(tmp) {
		n = len(tmp)
	}
	codecs := tmp[:n]

	formatted := make([]string, 0, len(codecs))
	for i, c := range codecs {
		formatted = append(formatted, fmt.Sprintf("%d) %s pt=%d", i+1, media.CanonicalSDPName(c), c.PayloadType))
	}

	if perr != nil {
		logger.Info("sip: audio codecs (partial)", "label", label, "codecs", formatted, "parse_error", perr.Error())
		return
	}
	logger.Info("sip: audio codecs", "label", label, "codecs", formatted)
}

func logCodecPrefs(logger *slog.Logger, label string, codecs []media.Codec) {
	if logger == nil || len(codecs) == 0 {
		return
	}
	formatted := make([]string, 0, len(codecs))
	for i, c := range codecs {
		formatted = append(formatted, fmt.Sprintf("%d) %s pt=%d", i+1, media.CanonicalSDPName(c), c.PayloadType))
	}
	logger.Info("sip: codec list", "label", label, "codecs", formatted)
}
