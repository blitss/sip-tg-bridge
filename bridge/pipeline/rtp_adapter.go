package pipeline

import (
	"github.com/emiago/diago/media"
	"github.com/pion/rtp"
)

type diagoRTPWriterAdapter struct {
	w media.RTPWriter
}

func (d *diagoRTPWriterAdapter) String() string {
	return "DiagoRTPWriter"
}

func (d *diagoRTPWriterAdapter) WriteRTP(h *rtp.Header, payload []byte) (int, error) {
	if d.w == nil {
		return 0, nil
	}
	pkt := &rtp.Packet{
		Header:  *h,
		Payload: payload,
	}
	if err := d.w.WriteRTP(pkt); err != nil {
		return 0, err
	}
	return len(payload), nil
}
