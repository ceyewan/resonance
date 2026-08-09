package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
)

const (
	messageIdempotencyDomain = "resonance.chat-message.idempotency.v1"
	maxClientMessageIDBytes  = 64
)

// canonicalMessage 只复制当前服务理解的字段，防止未知 protobuf 字段改变幂等语义。
func canonicalMessage(message *commonv1.Message) (*commonv1.Message, error) {
	if message == nil {
		return nil, fmt.Errorf("message is required")
	}

	messageType := message.GetType()
	switch messageType {
	case commonv1.MessageType_MESSAGE_TYPE_UNSPECIFIED:
		messageType = commonv1.MessageType_MESSAGE_TYPE_TEXT
	case commonv1.MessageType_MESSAGE_TYPE_TEXT,
		commonv1.MessageType_MESSAGE_TYPE_IMAGE,
		commonv1.MessageType_MESSAGE_TYPE_FILE,
		commonv1.MessageType_MESSAGE_TYPE_SYSTEM,
		commonv1.MessageType_MESSAGE_TYPE_AI_STREAM:
	default:
		return nil, fmt.Errorf("unsupported message type: %d", messageType)
	}

	clientMessageID := message.GetClientMsgId()
	if len(clientMessageID) > maxClientMessageIDBytes {
		return nil, fmt.Errorf("client_msg_id exceeds %d bytes", maxClientMessageIDBytes)
	}
	if clientMessageID != "" {
		if !utf8.ValidString(clientMessageID) {
			return nil, fmt.Errorf("client_msg_id is not valid UTF-8")
		}
		hasVisible := false
		for _, r := range clientMessageID {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("client_msg_id contains control characters")
			}
			if !unicode.IsSpace(r) {
				hasVisible = true
			}
		}
		if !hasVisible {
			return nil, fmt.Errorf("client_msg_id cannot contain only whitespace")
		}
	}

	return &commonv1.Message{
		Type:               messageType,
		Content:            message.GetContent(),
		ReplyToEventId:     message.GetReplyToEventId(),
		ClientMsgId:        clientMessageID,
		MentionedUsernames: append([]string(nil), message.GetMentionedUsernames()...),
	}, nil
}

func messageIdempotencyHash(sessionID, senderUsername string, message *commonv1.Message) (string, error) {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("marshal canonical message: %w", err)
	}

	digest := sha256.New()
	writeHashField(digest, []byte(messageIdempotencyDomain))
	writeHashField(digest, []byte(sessionID))
	writeHashField(digest, []byte(senderUsername))
	writeHashField(digest, payload)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeHashField(dst hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = dst.Write(size[:])
	_, _ = dst.Write(value)
}
