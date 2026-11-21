package requestaudit

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	codecRaw = iota
	codecZstd
)

func eventCodec(e auditEvent) int {
	if e.encoded != nil {
		return codecZstd
	}
	return codecRaw
}

func eventPayload(e auditEvent) []byte {
	if e.encoded != nil {
		return e.encoded
	}
	return e.raw
}

// Each persisted block is independently decodable for bounded Range reads.
func decodeBlock(codec, size int, data []byte) ([]byte, error) {
	if size < 0 || size > ChunkBytes {
		return nil, fmt.Errorf("invalid audit block length")
	}
	switch codec {
	case codecRaw:
	case codecZstd:
		decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxWindow(ChunkBytes), zstd.WithDecodeAllCapLimit(true))
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		data, err = decoder.DecodeAll(data, make([]byte, 0, size))
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid audit block encoding")
	}
	if len(data) != size {
		return nil, fmt.Errorf("invalid audit block length")
	}
	return data, nil
}

type bodyCodec struct {
	once sync.Once
	enc  *zstd.Encoder
}

func WithBodyCompression() Option {
	return func(m *Manager) {
		m.bodyCodec = &bodyCodec{}
	}
}

func (c *bodyCodec) encode(raw []byte) (int, []byte) {
	if c == nil || len(raw) < 256 {
		return codecRaw, raw
	}
	c.once.Do(func() {
		c.enc, _ = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1),
			zstd.WithWindowSize(ChunkBytes), zstd.WithEncoderLevel(zstd.SpeedDefault))
	})
	if c.enc != nil {
		packed := c.enc.EncodeAll(raw, nil)
		if len(packed)*10 <= len(raw)*9 {
			return codecZstd, packed
		}
	}
	return codecRaw, raw
}
