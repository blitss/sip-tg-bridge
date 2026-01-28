package bridge

// Ensure LiveKit media-sdk codecs are registered.
// media-sdk codecs self-register via init() when imported.

import (
	_ "github.com/livekit/media-sdk/dtmf"
	_ "github.com/livekit/media-sdk/g711"
	_ "github.com/livekit/media-sdk/g722"
)
