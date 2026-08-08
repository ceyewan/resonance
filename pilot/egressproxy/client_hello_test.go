package egressproxy

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadClientHelloRequiresExactSNIAndAllowedALPN(t *testing.T) {
	valid := captureTLSClientHello(t, "api.anthropic.com", []string{"h2", "http/1.1"})
	raw, err := readClientHello(bytes.NewReader(valid), "api.anthropic.com", 64<<10)
	require.NoError(t, err)
	require.Equal(t, valid, raw)

	tests := []struct {
		name      string
		host      string
		protocols []string
		payload   []byte
	}{
		{name: "no SNI", protocols: []string{"h2"}},
		{name: "wrong SNI", host: "console.anthropic.com", protocols: []string{"h2"}},
		{name: "uppercase SNI", host: "API.ANTHROPIC.COM", protocols: []string{"h2"}},
		{name: "no ALPN", host: "api.anthropic.com"},
		{name: "forbidden ALPN", host: "api.anthropic.com", protocols: []string{"h3"}},
		{name: "ordinary bytes", payload: []byte("GET / HTTP/1.1\r\n\r\n")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := test.payload
			if payload == nil {
				payload = captureTLSClientHello(t, test.host, test.protocols)
			}
			_, err := readClientHello(bytes.NewReader(payload), "api.anthropic.com", 64<<10)
			require.Error(t, err)
		})
	}
}

func TestReadClientHelloAcceptsTLSRecordAndTCPFragmentation(t *testing.T) {
	original := captureTLSClientHello(t, "api.anthropic.com", []string{"h2"})
	fragmented := fragmentFirstTLSRecord(t, original, 17)
	reader, writer := net.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	go func() {
		for offset := 0; offset < len(fragmented); {
			end := offset + 3
			if end > len(fragmented) {
				end = len(fragmented)
			}
			_, _ = writer.Write(fragmented[offset:end])
			offset = end
		}
	}()
	raw, err := readClientHello(reader, "api.anthropic.com", 64<<10)
	require.NoError(t, err)
	require.Equal(t, fragmented, raw)
}

func TestReadClientHelloRejectsOversizeBeforeAllocation(t *testing.T) {
	header := []byte{tlsHandshakeType, 3, 1, 0x10, 0x00}
	_, err := readClientHello(bytes.NewReader(header), "api.anthropic.com", 1024)
	require.Error(t, err)
}

func captureTLSClientHello(t *testing.T, serverName string, protocols []string) []byte {
	t.Helper()
	connection := &captureConn{}
	client := tls.Client(connection, &tls.Config{
		ServerName: serverName, NextProtos: protocols, InsecureSkipVerify: true, // test-only capture; no server is contacted
	})
	_ = client.Handshake()
	require.NotEmpty(t, connection.Bytes())
	return append([]byte(nil), connection.Bytes()...)
}

func fragmentFirstTLSRecord(t *testing.T, record []byte, split int) []byte {
	t.Helper()
	require.GreaterOrEqual(t, len(record), tlsRecordHeaderLength)
	length := int(binary.BigEndian.Uint16(record[3:5]))
	require.Equal(t, len(record), tlsRecordHeaderLength+length)
	require.Greater(t, split, 0)
	require.Less(t, split, length)
	first := append([]byte(nil), record[:tlsRecordHeaderLength]...)
	binary.BigEndian.PutUint16(first[3:5], uint16(split))
	first = append(first, record[tlsRecordHeaderLength:tlsRecordHeaderLength+split]...)
	second := append([]byte(nil), record[:tlsRecordHeaderLength]...)
	binary.BigEndian.PutUint16(second[3:5], uint16(length-split))
	second = append(second, record[tlsRecordHeaderLength+split:]...)
	return append(first, second...)
}

type captureConn struct{ bytes.Buffer }

func (c *captureConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *captureConn) Close() error                     { return nil }
func (c *captureConn) LocalAddr() net.Addr              { return captureAddr("local") }
func (c *captureConn) RemoteAddr() net.Addr             { return captureAddr("remote") }
func (c *captureConn) SetDeadline(time.Time) error      { return nil }
func (c *captureConn) SetReadDeadline(time.Time) error  { return nil }
func (c *captureConn) SetWriteDeadline(time.Time) error { return nil }

type captureAddr string

func (a captureAddr) Network() string { return "capture" }
func (a captureAddr) String() string  { return string(a) }
