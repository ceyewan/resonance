package pi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecoder_FrameLargerThanScannerLimit(t *testing.T) {
	payload := strings.Repeat("x", 256<<10)
	frame, err := json.Marshal(map[string]string{"type": "message_update", "payload": payload})
	require.NoError(t, err)
	frame = append(frame, '\n')

	decoder := NewDecoder(bytes.NewReader(frame), len(frame), int64(len(frame)))
	raw, err := decoder.Next()
	require.NoError(t, err)
	require.Contains(t, string(raw), payload[:128])
}

func TestDecoder_FragmentedUTF8AndUnicodeSeparators(t *testing.T) {
	input := "{\"type\":\"message_update\",\"text\":\"你🙂\u2028中\u2029文\"}\n"
	decoder := NewDecoder(&chunkReader{data: []byte(input), sizes: []int{1, 2, 3}}, 1024, 2048)

	raw, err := decoder.Next()
	require.NoError(t, err)
	var decoded map[string]string
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "你🙂\u2028中\u2029文", decoded["text"])
	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestDecoder_CRLFAndFinalFrameWithoutLF(t *testing.T) {
	decoder := NewDecoder(strings.NewReader("{\"type\":\"one\"}\r\n{\"type\":\"two\"}"), 128, 256)

	first, err := decoder.Next()
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"one"}`, string(first))
	second, err := decoder.Next()
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"two"}`, string(second))
	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestDecoder_ExactFrameLimitWithCRLF(t *testing.T) {
	frame := []byte(`{"type":"one"}`)
	input := append(append(append([]byte(nil), frame...), '\r'), '\n')
	decoder := NewDecoder(bytes.NewReader(input), len(frame), int64(len(input)))

	raw, err := decoder.Next()
	require.NoError(t, err)
	require.Equal(t, frame, []byte(raw))
}

func TestDecoder_FrameAndTotalLimits(t *testing.T) {
	frame := []byte(`{"type":"x"}`)

	decoder := NewDecoder(bytes.NewReader(append(append([]byte(nil), frame...), '\n')), len(frame), int64(len(frame)+1))
	_, err := decoder.Next()
	require.NoError(t, err)

	decoder = NewDecoder(strings.NewReader(`{"type":"xx"}`+"\n"), len(frame), 1024)
	_, err = decoder.Next()
	require.ErrorIs(t, err, ErrFrameTooLarge)

	decoder = NewDecoder(strings.NewReader(`{"type":"x"}`+"\n"), 1024, int64(len(frame)))
	_, err = decoder.Next()
	require.ErrorIs(t, err, ErrOutputTooLarge)
}

func TestDecoder_RejectsMalformedJSONAndStdoutPollution(t *testing.T) {
	for _, input := range []string{
		"\n",
		"pi starting...\n",
		"{not-json}\n",
		"[1,2,3]\n",
	} {
		t.Run(input, func(t *testing.T) {
			decoder := NewDecoder(strings.NewReader(input), 1024, 2048)
			_, err := decoder.Next()
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrMalformedJSON), "unexpected error: %v", err)
		})
	}
}

type chunkReader struct {
	data  []byte
	sizes []int
	next  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	size := r.sizes[r.next%len(r.sizes)]
	r.next++
	if size > len(p) {
		size = len(p)
	}
	if size > len(r.data) {
		size = len(r.data)
	}
	copy(p, r.data[:size])
	r.data = r.data[size:]
	return size, nil
}
