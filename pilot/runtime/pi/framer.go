package pi

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	defaultMaxFrameBytes  = 8 << 20
	defaultMaxOutputBytes = 64 << 20
)

// Decoder 按 Pi RPC 的严格 LF JSONL 语义读取 stdout。
// 它不会把 U+2028/U+2029 当作分隔符。
type Decoder struct {
	reader         *bufio.Reader
	maxFrameBytes  int
	maxOutputBytes int64
	totalBytes     int64
}

// NewDecoder 创建带单帧和累计输出硬限制的 Decoder。
func NewDecoder(r io.Reader, maxFrameBytes int, maxOutputBytes int64) *Decoder {
	if maxFrameBytes <= 0 {
		maxFrameBytes = defaultMaxFrameBytes
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = defaultMaxOutputBytes
	}
	return &Decoder{
		reader:         bufio.NewReaderSize(r, 32<<10),
		maxFrameBytes:  maxFrameBytes,
		maxOutputBytes: maxOutputBytes,
	}
}

// Next 返回下一条已经验证为 JSON object 的原始 frame。
// EOF 前没有 LF 的最后一帧也会按照 Pi 官方客户端示例解析。
func (d *Decoder) Next() (json.RawMessage, error) {
	frame, err := d.readFrame()
	if err != nil {
		return nil, err
	}

	if len(frame) == 0 {
		return nil, &ProtocolError{Kind: ErrMalformedJSON, Preview: "empty frame"}
	}
	if !utf8.Valid(frame) {
		return nil, &ProtocolError{Kind: ErrMalformedJSON, Preview: "invalid utf-8"}
	}

	var value map[string]json.RawMessage
	if err := json.Unmarshal(frame, &value); err != nil || value == nil {
		return nil, &ProtocolError{
			Kind:    ErrMalformedJSON,
			Preview: safePreview(frame),
			Cause:   err,
		}
	}
	return json.RawMessage(frame), nil
}

func (d *Decoder) readFrame() ([]byte, error) {
	frame := make([]byte, 0, 1024)
	for {
		fragment, err := d.reader.ReadSlice('\n')
		if len(fragment) > 0 {
			d.totalBytes += int64(len(fragment))
			if d.totalBytes > d.maxOutputBytes {
				return nil, &ProtocolError{Kind: ErrOutputTooLarge}
			}
			frame = append(frame, fragment...)
		}

		switch {
		case err == nil:
			frame = frame[:len(frame)-1]
			if len(frame) > 0 && frame[len(frame)-1] == '\r' {
				frame = frame[:len(frame)-1]
			}
			if len(frame) > d.maxFrameBytes {
				return nil, &ProtocolError{Kind: ErrFrameTooLarge}
			}
			return frame, nil
		case errors.Is(err, bufio.ErrBufferFull):
			if len(frame) > d.maxFrameBytes {
				return nil, &ProtocolError{Kind: ErrFrameTooLarge}
			}
			continue
		case errors.Is(err, io.EOF):
			if len(frame) == 0 {
				return nil, io.EOF
			}
			if len(frame) > 0 && frame[len(frame)-1] == '\r' {
				frame = frame[:len(frame)-1]
			}
			if len(frame) > d.maxFrameBytes {
				return nil, &ProtocolError{Kind: ErrFrameTooLarge}
			}
			return frame, nil
		default:
			return nil, fmt.Errorf("read pi rpc stdout: %w", err)
		}
	}
}

func safePreview(frame []byte) string {
	digest := sha256.Sum256(frame)
	return fmt.Sprintf("bytes=%d sha256=%x", len(frame), digest[:8])
}
