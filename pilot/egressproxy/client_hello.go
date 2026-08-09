package egressproxy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	tlsRecordHeaderLength = 5
	maxTLSRecordPayload   = 16 << 10
	tlsHandshakeType      = 22
	clientHelloType       = 1
	serverNameExtension   = 0
	alpnExtension         = 16
)

// readClientHello reads complete TLS handshake records up to and including one
// ClientHello. It returns the original record bytes so the proxy can forward
// them without decrypting or rewriting any TLS data.
func readClientHello(reader io.Reader, expectedHost string, maxBytes int) ([]byte, error) {
	var raw bytes.Buffer
	handshake := make([]byte, 0, 4096)
	wantedHandshakeBytes := -1

	for {
		if raw.Len()+tlsRecordHeaderLength > maxBytes {
			return nil, fmt.Errorf("TLS ClientHello exceeds limit")
		}
		header := make([]byte, tlsRecordHeaderLength)
		if _, err := io.ReadFull(reader, header); err != nil {
			return nil, fmt.Errorf("read TLS record header: %w", err)
		}
		if header[0] != tlsHandshakeType || header[1] != 3 || header[2] < 1 || header[2] > 4 {
			return nil, fmt.Errorf("first tunnel payload is not a TLS handshake")
		}
		recordLength := int(binary.BigEndian.Uint16(header[3:5]))
		if recordLength == 0 || recordLength > maxTLSRecordPayload || raw.Len()+tlsRecordHeaderLength+recordLength > maxBytes {
			return nil, fmt.Errorf("TLS ClientHello record length is invalid")
		}
		payload := make([]byte, recordLength)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, fmt.Errorf("read TLS record payload: %w", err)
		}
		raw.Write(header)
		raw.Write(payload)
		handshake = append(handshake, payload...)

		if wantedHandshakeBytes < 0 && len(handshake) >= 4 {
			if handshake[0] != clientHelloType {
				return nil, fmt.Errorf("first TLS handshake message is not ClientHello")
			}
			helloLength := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
			wantedHandshakeBytes = 4 + helloLength
			if helloLength == 0 || wantedHandshakeBytes > maxBytes {
				return nil, fmt.Errorf("TLS ClientHello message length is invalid")
			}
		}
		if wantedHandshakeBytes >= 0 && len(handshake) >= wantedHandshakeBytes {
			if len(handshake) != wantedHandshakeBytes {
				return nil, fmt.Errorf("unexpected data follows TLS ClientHello")
			}
			if err := validateClientHello(handshake[4:], expectedHost); err != nil {
				return nil, err
			}
			return raw.Bytes(), nil
		}
	}
}

func validateClientHello(hello []byte, expectedHost string) error {
	cursor := helloCursor{data: hello}
	legacyVersion, ok := cursor.take(2)
	if !ok || legacyVersion[0] != 3 || legacyVersion[1] < 1 || legacyVersion[1] > 3 {
		return fmt.Errorf("TLS ClientHello legacy version is invalid")
	}
	if _, ok := cursor.take(32); !ok { // random
		return fmt.Errorf("TLS ClientHello is truncated")
	}
	sessionLength, ok := cursor.uint8()
	if !ok || !cursor.skip(int(sessionLength)) {
		return fmt.Errorf("TLS ClientHello session ID is truncated")
	}
	cipherLength, ok := cursor.uint16()
	if !ok || cipherLength < 2 || cipherLength%2 != 0 || !cursor.skip(int(cipherLength)) {
		return fmt.Errorf("TLS ClientHello cipher suites are invalid")
	}
	compressionLength, ok := cursor.uint8()
	compressionMethods, hasCompression := cursor.take(int(compressionLength))
	if !ok || !hasCompression || len(compressionMethods) != 1 || compressionMethods[0] != 0 {
		return fmt.Errorf("TLS ClientHello compression methods are invalid")
	}
	extensionLength, ok := cursor.uint16()
	if !ok || int(extensionLength) != cursor.remaining() {
		return fmt.Errorf("TLS ClientHello extensions are invalid")
	}
	extensions, _ := cursor.take(int(extensionLength))
	extensionCursor := helloCursor{data: extensions}
	seenSNI, seenALPN := false, false
	for extensionCursor.remaining() > 0 {
		extensionType, ok := extensionCursor.uint16()
		if !ok {
			return fmt.Errorf("TLS extension type is truncated")
		}
		length, ok := extensionCursor.uint16()
		if !ok {
			return fmt.Errorf("TLS extension length is truncated")
		}
		payload, ok := extensionCursor.take(int(length))
		if !ok {
			return fmt.Errorf("TLS extension payload is truncated")
		}
		switch extensionType {
		case serverNameExtension:
			if seenSNI {
				return fmt.Errorf("TLS ClientHello contains duplicate SNI")
			}
			seenSNI = true
			if err := validateServerNameExtension(payload, expectedHost); err != nil {
				return err
			}
		case alpnExtension:
			if seenALPN {
				return fmt.Errorf("TLS ClientHello contains duplicate ALPN")
			}
			seenALPN = true
			if err := validateALPNExtension(payload); err != nil {
				return err
			}
		}
	}
	if !seenSNI {
		return fmt.Errorf("TLS ClientHello SNI is required")
	}
	if !seenALPN {
		return fmt.Errorf("TLS ClientHello ALPN is required")
	}
	return nil
}

func validateServerNameExtension(payload []byte, expectedHost string) error {
	cursor := helloCursor{data: payload}
	listLength, ok := cursor.uint16()
	if !ok || int(listLength) != cursor.remaining() || listLength == 0 {
		return fmt.Errorf("TLS SNI list is invalid")
	}
	nameType, ok := cursor.uint8()
	if !ok || nameType != 0 {
		return fmt.Errorf("TLS SNI must contain exactly one DNS host_name")
	}
	nameLength, ok := cursor.uint16()
	nameBytes, hasName := cursor.take(int(nameLength))
	if !ok || !hasName || cursor.remaining() != 0 || len(nameBytes) == 0 {
		return fmt.Errorf("TLS SNI host_name is invalid")
	}
	name := string(nameBytes)
	canonical, err := canonicalDNSName(name)
	if err != nil || name != canonical || canonical != expectedHost {
		return fmt.Errorf("TLS SNI does not match CONNECT host")
	}
	return nil
}

func validateALPNExtension(payload []byte) error {
	cursor := helloCursor{data: payload}
	listLength, ok := cursor.uint16()
	if !ok || int(listLength) != cursor.remaining() || listLength == 0 {
		return fmt.Errorf("TLS ALPN list is invalid")
	}
	seen := make(map[string]struct{}, 2)
	for cursor.remaining() > 0 {
		protocolLength, ok := cursor.uint8()
		protocol, hasProtocol := cursor.take(int(protocolLength))
		if !ok || !hasProtocol || len(protocol) == 0 {
			return fmt.Errorf("TLS ALPN protocol is invalid")
		}
		name := string(protocol)
		if name != "h2" && name != "http/1.1" {
			return fmt.Errorf("TLS ALPN protocol is not allowed")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("TLS ALPN protocol is duplicated")
		}
		seen[name] = struct{}{}
	}
	return nil
}

type helloCursor struct {
	data   []byte
	offset int
}

func (c *helloCursor) remaining() int { return len(c.data) - c.offset }

func (c *helloCursor) take(length int) ([]byte, bool) {
	if length < 0 || length > c.remaining() {
		return nil, false
	}
	value := c.data[c.offset : c.offset+length]
	c.offset += length
	return value, true
}

func (c *helloCursor) skip(length int) bool {
	_, ok := c.take(length)
	return ok
}

func (c *helloCursor) uint8() (uint8, bool) {
	value, ok := c.take(1)
	if !ok {
		return 0, false
	}
	return value[0], true
}

func (c *helloCursor) uint16() (uint16, bool) {
	value, ok := c.take(2)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint16(value), true
}
