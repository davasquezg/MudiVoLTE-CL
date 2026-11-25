package media

import (
	"fmt"
	"strconv"
	"strings"
)

// Codec turns one packet's worth of PCM (mono s16 at SampleRate) into an RTP
// payload and back.
type Codec interface {
	Name() string
	SampleRate() int // also the RTP clock rate for our codecs
	Encode(pcm []int16) ([]byte, error)
	Decode(payload []byte) ([]int16, error)
}

// FrameCoder is an AMR-WB or AMR speech codec working on storage-format
// frames (RFC 4867 §5.3) of 20 ms: 320 samples at 16 kHz for AMR-WB
// (opencore-amrwb + vo-amrwbenc via cgo), 160 samples at 8 kHz for AMR
// (opencore-amrnb via cgo).
type FrameCoder interface {
	EncodeFrame(pcm []int16, mode int) ([]byte, error) // one frame of samples → storage frame
	DecodeFrame(storage []byte) ([]int16, error)       // storage frame → one frame of samples
	Close()
}

// NewAMRWBCoder is set by a codec implementation package (see media/amrwb).
var NewAMRWBCoder func() (FrameCoder, error)

// NewAMRNBCoder is set by a codec implementation package (see media/amrnb).
var NewAMRNBCoder func() (FrameCoder, error)

// Params selects the codec for a negotiated stream.
type Params struct {
	Name      string // AMR-WB, AMR, PCMA, PCMU
	ClockRate int
	Fmtp      string
	Ptime     int // ms; 0 → 20
}

func (p Params) fmtp(name string) (string, bool) {
	for _, kv := range strings.Split(p.Fmtp, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(kv), "=")
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// NewCodec builds the codec for p.
func NewCodec(p Params) (Codec, error) {
	ptime := p.Ptime
	if ptime == 0 {
		ptime = 20
	}
	switch strings.ToUpper(p.Name) {
	case "PCMA":
		return &g711{alaw: true, samples: 8 * ptime}, nil
	case "PCMU":
		return &g711{samples: 8 * ptime}, nil
	case "AMR-WB":
		if NewAMRWBCoder == nil {
			return nil, fmt.Errorf("AMR-WB codec not linked in (import github.com/xarvel/MudiVoLTE/volte/media/amrwb)")
		}
		return newAMRCodec(p, ptime, &amrwb, NewAMRWBCoder, 16000)
	case "AMR":
		if NewAMRNBCoder == nil {
			return nil, fmt.Errorf("AMR codec not linked in (import github.com/xarvel/MudiVoLTE/volte/media/amrnb)")
		}
		return newAMRCodec(p, ptime, &amrnb, NewAMRNBCoder, 8000)
	}
	return nil, fmt.Errorf("unsupported codec %q", p.Name)
}

// newAMRCodec builds AMR-WB or AMR (both RFC 4867) at rate Hz.
func newAMRCodec(p Params, ptime int, f *amrFormat, newCoder func() (FrameCoder, error), rate int) (Codec, error) {
	fc, err := newCoder()
	if err != nil {
		return nil, err
	}
	// The highest speech mode is the one below SID: 8 (23.85) for AMR-WB,
	// 7 (12.2) for AMR.
	top := int(f.sid) - 1
	c := &amrCodec{f: f, fc: fc, rate: rate, frames: ptime / 20, mode: top}
	if v, ok := p.fmtp("octet-align"); ok && v == "1" {
		c.octetAligned = true
	}
	// Send in the highest mode the peer allows (RFC 4867 §8.1 mode-set).
	if v, ok := p.fmtp("mode-set"); ok {
		best := -1
		for _, m := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(m)); err == nil && n >= 0 && n <= top && n > best {
				best = n
			}
		}
		if best >= 0 {
			c.mode = best
		}
	}
	if c.frames < 1 {
		c.frames = 1
	}
	return c, nil
}

type g711 struct {
	alaw    bool
	samples int
}

func (g *g711) Name() string {
	if g.alaw {
		return "PCMA"
	}
	return "PCMU"
}

func (g *g711) SampleRate() int { return 8000 }

func (g *g711) Encode(pcm []int16) ([]byte, error) {
	out := make([]byte, len(pcm))
	for i, s := range pcm {
		if g.alaw {
			out[i] = LinearToALaw(s)
		} else {
			out[i] = LinearToULaw(s)
		}
	}
	return out, nil
}

func (g *g711) Decode(p []byte) ([]int16, error) {
	out := make([]int16, len(p))
	for i, b := range p {
		if g.alaw {
			out[i] = ALawToLinear(b)
		} else {
			out[i] = ULawToLinear(b)
		}
	}
	return out, nil
}

// amrCodec is AMR-WB or AMR: 20 ms frames from a FrameCoder, packed per
// RFC 4867 (bandwidth-efficient or octet-aligned).
type amrCodec struct {
	f            *amrFormat
	fc           FrameCoder
	rate         int // 16000 for AMR-WB, 8000 for AMR
	frames       int
	mode         int
	octetAligned bool
}

func (c *amrCodec) Name() string    { return c.f.name }
func (c *amrCodec) SampleRate() int { return c.rate }

// frameSamples is one 20 ms frame: 320 samples for AMR-WB, 160 for AMR.
func (c *amrCodec) frameSamples() int { return c.rate / 50 }

func (c *amrCodec) Encode(pcm []int16) ([]byte, error) {
	n := c.frameSamples()
	if len(pcm) != n*c.frames {
		return nil, fmt.Errorf("%s needs %d samples, got %d", c.f.name, n*c.frames, len(pcm))
	}
	frames := make([]Frame, c.frames)
	for i := range frames {
		st, err := c.fc.EncodeFrame(pcm[i*n:(i+1)*n], c.mode)
		if err != nil {
			return nil, err
		}
		if frames[i], err = c.f.fromStorage(st); err != nil {
			return nil, err
		}
	}
	return c.f.pack(frames, CMRNoPref, c.octetAligned)
}

func (c *amrCodec) Decode(p []byte) ([]int16, error) {
	frames, _, err := c.f.unpack(p, c.octetAligned)
	if err != nil {
		return nil, err
	}
	var out []int16
	for _, f := range frames {
		if f.FT == FTNoData {
			// Nothing transmitted: let the decoder conceal (DTX/comfort noise).
			f = Frame{FT: FTNoData}
		}
		pcm, err := c.fc.DecodeFrame(c.f.storage(f))
		if err != nil {
			return nil, err
		}
		out = append(out, pcm...)
	}
	return out, nil
}

// Close releases codec state.
func Close(c Codec) {
	if a, ok := c.(*amrCodec); ok {
		a.fc.Close()
	}
}
