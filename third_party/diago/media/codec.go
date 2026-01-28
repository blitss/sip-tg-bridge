// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package media

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emiago/diago/media/sdp"
	lkmedia "github.com/livekit/media-sdk"
	lkdtmf "github.com/livekit/media-sdk/dtmf"
	lkg711 "github.com/livekit/media-sdk/g711"
	_ "github.com/livekit/media-sdk/g722"
	lkrtp "github.com/livekit/media-sdk/rtp"
	lksdp "github.com/livekit/media-sdk/sdp"
)

const defaultSampleDur = 20 * time.Millisecond

func DefaultSampleDur() time.Duration {
	return defaultSampleDur
}

type Codec struct {
	Name        string
	PayloadType uint8
	SampleRate  uint32
	SampleDur   time.Duration
	NumChannels int // 1 or 2
}

func (c *Codec) String() string {
	return fmt.Sprintf("name=%s pt=%d rate=%d dur=%s channels=%d", c.Name, c.PayloadType, c.SampleRate, c.SampleDur.String(), c.NumChannels)
}

// SDPName returns codec name in SDP rtpmap format: "<name>/<rate>[/<channels>]".
// This matches the format used by LiveKit media-sdk CodecInfo.SDPName.
func (c *Codec) SDPName() string {
	if c == nil {
		return ""
	}
	if c.NumChannels > 0 && c.NumChannels != 1 {
		return fmt.Sprintf("%s/%d/%d", c.Name, c.SampleRate, c.NumChannels)
	}
	return fmt.Sprintf("%s/%d", c.Name, c.SampleRate)
}

// CanonicalSDPName returns a canonical SDP rtpmap name string for well-known codecs.
//
// We intentionally normalize casing for static codecs (PCMU/PCMA/G722), and always
// include channel count for Opus to match how LiveKit media-sdk registers Opus.
func CanonicalSDPName(c Codec) string {
	name := strings.ToLower(strings.TrimSpace(c.Name))
	if c.SampleRate == 0 {
		// Best-effort fallback; keep existing formatting.
		return c.SDPName()
	}
	ch := c.NumChannels
	if ch <= 0 {
		ch = 1
	}
	switch name {
	case "pcmu":
		return "PCMU/8000"
	case "pcma":
		return "PCMA/8000"
	case "g722":
		// SDP rtpmap uses 8000 clock for G722.
		return "G722/8000"
	case "opus":
		// media-sdk registers Opus as "opus/48000/<channels>" (no alias for opus/48000).
		return fmt.Sprintf("opus/%d/%d", c.SampleRate, ch)
	default:
		// Preserve original name casing for unknown codecs.
		if ch != 1 {
			return fmt.Sprintf("%s/%d/%d", c.Name, c.SampleRate, ch)
		}
		return fmt.Sprintf("%s/%d", c.Name, c.SampleRate)
	}
}

func (c *Codec) IsDTMF() bool {
	if c == nil {
		return false
	}
	n := c.SDPName()
	return strings.EqualFold(n, lkdtmf.SDPName) || strings.EqualFold(n, lkdtmf.SDPName+"/1")
}

func offerPayloadTypeBySDPName(sdpName string) (uint8, bool) {
	sdpName = strings.TrimSpace(sdpName)
	if sdpName == "" {
		return 0, false
	}
	for _, ci := range lksdp.OfferCodecs() {
		if strings.EqualFold(ci.Codec.Info().SDPName, sdpName) {
			return uint8(ci.Type), true
		}
	}
	return 0, false
}

func codecFromLK(codec lkmedia.Codec, payloadType uint8, sampleDur time.Duration) (Codec, bool) {
	if codec == nil {
		return Codec{}, false
	}
	if sampleDur <= 0 {
		sampleDur = defaultSampleDur
	}
	info := codec.Info()
	return CodecFromSDPName(info.SDPName, payloadType, sampleDur)
}

func CodecAudioUlaw(sampleDur time.Duration) Codec {
	c := lksdp.CodecByName(lkg711.ULawSDPName)
	if out, ok := codecFromLK(c, uint8(c.Info().RTPDefType), sampleDur); ok {
		return out
	}
	panic("failed to load PCMU codec from LiveKit media-sdk registry")
}

func CodecAudioAlaw(sampleDur time.Duration) Codec {
	c := lksdp.CodecByName(lkg711.ALawSDPName)
	if out, ok := codecFromLK(c, uint8(c.Info().RTPDefType), sampleDur); ok {
		return out
	}
	panic("failed to load PCMA codec from LiveKit media-sdk registry")
}

func CodecTelephoneEvent8000(sampleDur time.Duration) Codec {
	pt, ok := offerPayloadTypeBySDPName(lkdtmf.SDPName)
	if !ok {
		panic("failed to resolve telephone-event payload type from LiveKit media-sdk OfferCodecs")
	}
	out, ok := CodecFromSDPName(lkdtmf.SDPName, pt, sampleDur)
	if !ok {
		panic("failed to build telephone-event codec from LiveKit media-sdk")
	}
	return out
}

// CodecFromSDPName parses LiveKit/media-sdk-style SDP codec name strings like:
// - "PCMU/8000"
// - "PCMA/8000"
// - "G722/8000"
// - "opus/48000/2"
// - "telephone-event/8000"
//
// It returns a diago Codec with Name/SampleRate/NumChannels filled, plus the provided
// payloadType and sampleDur.
func CodecFromSDPName(sdpName string, payloadType uint8, sampleDur time.Duration) (Codec, bool) {
	sdpName = strings.TrimSpace(sdpName)
	if sdpName == "" {
		return Codec{}, false
	}
	parts := strings.Split(sdpName, "/")
	if len(parts) < 2 {
		return Codec{}, false
	}

	name := parts[0]
	rate64, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || rate64 == 0 {
		return Codec{}, false
	}
	ch := 1
	if len(parts) >= 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil && n > 0 {
			ch = n
		}
	}

	if sampleDur <= 0 {
		sampleDur = 20 * time.Millisecond
	}
	return Codec{
		Name:        name,
		PayloadType: payloadType,
		SampleRate:  uint32(rate64),
		SampleDur:   sampleDur,
		NumChannels: ch,
	}, true
}

// SampleTimestamp returns number of samples as RTP Timestamp measure
func (c *Codec) SampleTimestamp() uint32 {
	return uint32(float64(c.SampleRate) * c.SampleDur.Seconds())
}

// Samples16 returns PCM 16 bit samples size
func (c *Codec) Samples16() int {
	return c.SamplesPCM(16)
}

// Samples is samples in pcm
func (c *Codec) SamplesPCM(bitSize int) int {
	return bitSize / 8 * int(float64(c.SampleRate)*c.SampleDur.Seconds()) * c.NumChannels
}

func CodecAudioFromSession(s *MediaSession) Codec {
	// Prefer negotiated codecs if available, otherwise choose from the configured list.
	if codec, ok := CodecAudioFromList(s.filterCodecs); ok {
		return codec
	}
	if codec, ok := CodecAudioFromList(s.Codecs); ok {
		return codec
	}
	return s.Codecs[0]
}

func CodecAudioFromList(codecs []Codec) (Codec, bool) {
	// NOTE: diago used to select "first in list". That follows RFC3264 advice
	// about preserving offer order, but it's not what we want for *local* codec
	// choice. We want best quality/priority among common codecs.
	//
	// We keep SDP ordering intact elsewhere; this function is only for selecting
	// the codec used by the media pipeline.
	bestIdx := -1
	bestPrio := -1 << 30
	for i, codec := range codecs {
		if codec.IsDTMF() || strings.EqualFold(codec.Name, "telephone-event") {
			continue
		}

		p := CodecPreferenceWeight(codec)
		if bestIdx == -1 || p > bestPrio {
			bestIdx = i
			bestPrio = p
		}
	}
	if bestIdx >= 0 {
		return codecs[bestIdx], true
	}
	return Codec{}, false
}

// CodecPreferenceWeight returns codec preference score.
//
// Higher is better. This intentionally follows the codec registry priority
// (as registered in LiveKit media-sdk) so callers can control preference by
// registration settings.
func CodecPreferenceWeight(c Codec) int {
	if c.IsDTMF() || strings.EqualFold(c.Name, "telephone-event") {
		return -1 << 20
	}

	if lk := lksdp.CodecByName(CanonicalSDPName(c)); lk != nil {
		return lk.Info().Priority
	}
	return -1000
}

// SortCodecsByPreference sorts codecs in-place by descending CodecPreferenceWeight.
// The sort is stable to preserve relative order when weights are equal.
func SortCodecsByPreference(codecs []Codec) {
	slices.SortStableFunc(codecs, func(a, b Codec) int {
		aw, bw := CodecPreferenceWeight(a), CodecPreferenceWeight(b)
		// Descending by weight.
		if aw != bw {
			return bw - aw
		}
		return 0
	})
}

// Deprecated: Use CodecAudioFromSession
func CodecFromSession(s *MediaSession) Codec {
	return CodecAudioFromSession(s)
}

// Deprecated: Use CodecAudioFromPayloadType
func CodecFromPayloadType(payloadType uint8) Codec {
	f := strconv.Itoa(int(payloadType))
	return mapSupportedCodec(f)
}

func CodecAudioFromPayloadType(payloadType uint8) (Codec, error) {
	// Prefer LiveKit media-sdk registry for static payload types.
	if c := lkrtp.CodecByPayloadType(byte(payloadType)); c != nil {
		if out, ok := codecFromLK(c, payloadType, defaultSampleDur); ok {
			return out, nil
		}
	}
	// DTMF is often negotiated as dynamic. If we are using the default offering
	// order, it is typically assigned 101.
	if pt, ok := offerPayloadTypeBySDPName(lkdtmf.SDPName); ok && pt == payloadType {
		return CodecTelephoneEvent8000(defaultSampleDur), nil
	}
	return Codec{}, fmt.Errorf("non supported codec: %d", payloadType)
}

func mapSupportedCodec(f string) Codec {
	// TODO: Here we need to be more explicit like matching sample rate, channels and other

	pt, err := sdp.FormatNumeric(f)
	if err != nil {
		slog.Warn("Format is non numeric value", "format", f)
	}
	// If it's a known static codec, use its SDP definition.
	if c := lkrtp.CodecByPayloadType(byte(pt)); c != nil {
		if out, ok := codecFromLK(c, pt, defaultSampleDur); ok {
			return out
		}
	}
	// Attempt to match the default DTMF offering payload type.
	if dt, ok := offerPayloadTypeBySDPName(lkdtmf.SDPName); ok && dt == pt {
		return CodecTelephoneEvent8000(defaultSampleDur)
	}

	slog.Warn("Unsupported format. Using default clock rate", "format", f)
	return Codec{
		PayloadType: pt,
		SampleRate:  8000,
		SampleDur:   defaultSampleDur,
		NumChannels: 1,
	}
}

// func CodecsFromSDP(log *slog.Logger, sd sdp.SessionDescription, codecsAudio []Codec) error {
// 	md, err := sd.MediaDescription("audio")
// 	if err != nil {
// 		return err
// 	}

// 	codecs := make([]Codec, len(md.Formats))
// 	attrs := sd.Values("a")
// 	n, err := CodecsFromSDPRead(log, md, attrs, codecs)
// 	if err != nil {
// 		return err
// 	}
// 	codecs = codecs[:n]
// }

// CodecsFromSDP will try to parse as much as possible, but it will return also error in case
// some properties could not be read
// You can take what is parsed or return error
func CodecsFromSDPRead(formats []string, attrs []string, codecsAudio []Codec) (int, error) {
	n := 0
	var rerr error

	// Parse ptime (ms) once; this affects codec intersection (SampleDur is part of Codec equality).
	ptimeDur := defaultSampleDur
	for _, a := range attrs {
		if strings.HasPrefix(a, "ptime:") {
			v := strings.TrimPrefix(a, "ptime:")
			if ms, err := strconv.Atoi(v); err == nil && ms > 0 && ms <= 1000 {
				ptimeDur = time.Duration(ms) * time.Millisecond
			}
			break
		}
	}

	for _, f := range formats {
		pt64, err := strconv.ParseUint(f, 10, 8)
		if err != nil {
			rerr = errors.Join(rerr, fmt.Errorf("format type failed to conv to integer, skipping f=%s: %w", f, err))
			continue
		}
		pt := uint8(pt64)

		// First try to locate rtpmap for this payload type.
		rtpmapPref := "rtpmap:" + f + " "
		rtpmapVal := ""
		for _, a := range attrs {
			if strings.HasPrefix(a, rtpmapPref) {
				rtpmapVal = strings.TrimSpace(a[len(rtpmapPref):])
				break
			}
		}
		if rtpmapVal != "" {
			// rtpmap value: "<encoding name>/<clock rate> [/<encoding params>]" possibly followed by more tokens.
			first := strings.Fields(rtpmapVal)
			if len(first) == 0 {
				rerr = errors.Join(rerr, fmt.Errorf("bad rtpmap property a=%s", rtpmapVal))
				continue
			}
			if codec, ok := CodecFromSDPName(first[0], pt, ptimeDur); ok {
				codecsAudio[n] = codec
				n++
				continue
			}
			rerr = errors.Join(rerr, fmt.Errorf("bad rtpmap codec name=%q", first[0]))
			continue
		}

		// For static payload types, rtpmap can be omitted; use LiveKit registry when possible.
		if c := lkrtp.CodecByPayloadType(byte(pt)); c != nil {
			if codec, ok := codecFromLK(c, pt, ptimeDur); ok {
				codecsAudio[n] = codec
				n++
				continue
			}
		}

		// Otherwise, fall back to a minimal codec definition.
		codecsAudio[n] = Codec{
			PayloadType: pt,
			SampleRate:  8000,
			SampleDur:   ptimeDur,
			NumChannels: 1,
		}
		n++

	}
	return n, nil
}
