//go:build cgo

// Package amrnb links the AMR (narrowband) speech codec from opencore-amrnb
// (encoder and decoder, Apache-2.0) and registers it with media. Import it
// for its side effect:
//
//	import _ "github.com/xarvel/MudiVoLTE/volte/media/amrnb"
//
// By default it links the distribution's shared runtime (link_shared.go).
// The amrwb_static build tag (the same tag as media/amrwb, so one static
// build gets both codecs) links the static archive instead (link_static.go);
// CGO_LDFLAGS must point at the directory holding libopencore-amrnb.a.
//
// Distributions ship only the .so.0 runtime without headers, so the
// interface of opencore-amr 0.1.6 (amrnb/interf_enc.h, amrnb/interf_dec.h)
// is declared here. Encoder_Interface_Encode takes an enum Mode (MR475 = 0 …
// MR122 = 7); a C enum with these values has the size and calling convention
// of int, so it is declared as int. Both functions work on frames in the
// RFC 4867 §5.3 storage format: the wrapper writes the frame type header
// (FT << 3 | Q) in front of the speech bits, and reads the frame type back
// from it on decoding.
package amrnb

/*
void *Encoder_Interface_init(int dtx);
int   Encoder_Interface_Encode(void *state, int mode, const short *speech, unsigned char *out, int forceSpeech);
void  Encoder_Interface_exit(void *state);
void *Decoder_Interface_init(void);
void  Decoder_Interface_Decode(void *state, const unsigned char *in, short *out, int bfi);
void  Decoder_Interface_exit(void *state);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/xarvel/MudiVoLTE/volte/media"
)

func init() { media.NewAMRNBCoder = New }

// frameSamples is 20 ms at 8 kHz.
const frameSamples = 160

// Coder holds one encoder and one decoder state.
type Coder struct {
	mu  sync.Mutex
	enc unsafe.Pointer
	dec unsafe.Pointer
}

// New allocates codec state. DTX is off: the encoder sends a speech frame
// every 20 ms, like the AMR-WB encoder of media/amrwb.
func New() (media.FrameCoder, error) {
	enc, dec := C.Encoder_Interface_init(0), C.Decoder_Interface_init()
	if enc == nil || dec == nil {
		if enc != nil {
			C.Encoder_Interface_exit(enc)
		}
		if dec != nil {
			C.Decoder_Interface_exit(dec)
		}
		return nil, errors.New("AMR codec init failed")
	}
	return &Coder{enc: enc, dec: dec}, nil
}

// EncodeFrame encodes 160 samples (20 ms at 8 kHz) to a storage frame in
// mode 0–7.
func (c *Coder) EncodeFrame(pcm []int16, mode int) ([]byte, error) {
	if len(pcm) != frameSamples {
		return nil, errors.New("AMR frame is 160 samples")
	}
	if mode < 0 || mode > media.ModeNB122 {
		return nil, errors.New("AMR mode out of range")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enc == nil {
		return nil, errors.New("codec closed")
	}
	// The largest frame is 12.2 kbit/s: 1 header byte + 31 bytes.
	var out [64]C.uchar
	n := C.Encoder_Interface_Encode(c.enc, C.int(mode), (*C.short)(unsafe.Pointer(&pcm[0])), &out[0], 0)
	if n <= 0 || n > C.int(len(out)) {
		return nil, errors.New("AMR encode failed")
	}
	return C.GoBytes(unsafe.Pointer(&out[0]), n), nil
}

// DecodeFrame decodes one storage frame to 160 samples.
func (c *Coder) DecodeFrame(storage []byte) ([]int16, error) {
	if len(storage) == 0 {
		return nil, errors.New("empty frame")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dec == nil {
		return nil, errors.New("codec closed")
	}
	in := make([]byte, 64) // decoder may read a full frame's worth
	copy(in, storage)
	out := make([]int16, frameSamples)
	C.Decoder_Interface_Decode(c.dec, (*C.uchar)(unsafe.Pointer(&in[0])), (*C.short)(unsafe.Pointer(&out[0])), 0)
	return out, nil
}

// Close frees the codec state.
func (c *Coder) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enc != nil {
		C.Encoder_Interface_exit(c.enc)
		c.enc = nil
	}
	if c.dec != nil {
		C.Decoder_Interface_exit(c.dec)
		c.dec = nil
	}
}
