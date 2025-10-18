package libnet

import (
	"encoding/binary"
	"bytes"
	"fmt"
	"io"
)

type MessageID byte

// https://wiki.theory.org/BitTorrentSpecification#Messages
const (
		// MsgKeepAlive	 is MessageID = nil
    MsgChoke         MessageID = 0 // <len=0001><id=0>
    MsgUnchoke       MessageID = 1 // <len=0001><id=1>
    MsgInterested    MessageID = 2 // <len=0001><id=2>
    MsgNotInterested MessageID = 3 // <len=0001><id=3>
    MsgHave          MessageID = 4 // <len=0005><id=4><piece index>
    MsgBitfield      MessageID = 5 // <len=0001+X><id=5><bitfield>
    MsgRequest       MessageID = 6 // <len=0013><id=6><index><begin><length>
    MsgPiece         MessageID = 7 // <len=0009+X><id=7><index><begin><block>
    MsgCancel        MessageID = 8 // <len=0013><id=8><index><begin><length>
    MsgPort          MessageID = 9 // <len=0003><id=9><listen-port>
)

type Message struct {
	ID      *MessageID // nil is keep-alive
	Payload []byte
}

func (m *Message) Serialize() ([]byte, error) {
	var buf bytes.Buffer

	if m.ID == nil {
		// Keep-alive message
		return []byte{0, 0, 0, 0}, nil
	}

	length := 1 + len(m.Payload) // 1 byte for message ID + payload
	if err := binary.Write(&buf, binary.BigEndian, uint32(length)); err != nil {
		return nil, err
	}

	buf.WriteByte(byte(*m.ID))
	buf.Write(m.Payload)

	return buf.Bytes(), nil
}

// Constructors for messages
func NewMessage(id MessageID, payload []byte) *Message {
	return &Message{ID: &id, Payload: payload}
}

// Simple no-payload messages (reuse this)
func NewSimpleMessage(id MessageID) *Message {
	return NewMessage(id, nil)
}

func NewHandshakeMessage(peerID [20]byte, infoHash [20]byte) []byte {
	pstr := "BitTorrent protocol"
	pstrlen := byte(len(pstr))
	reserved := make([]byte, 8) // all zeroes

	// Build the handshake message
	handshake := make([]byte, 0, 68)
	handshake = append(handshake, pstrlen)
	handshake = append(handshake, []byte(pstr)...)
	handshake = append(handshake, reserved...)
	handshake = append(handshake, infoHash[:]...)
	handshake = append(handshake, peerID[:]...)

	return handshake
}

// ReadMessage reads a peer wire protocol message from the connection.
// Message format: <length prefix><message ID><payload>
// - length prefix: 4 bytes, big-endian uint32
// - message ID: 1 byte (if length > 0)
// - payload: (length - 1) bytes
func ReadMessage(conn io.Reader) (*Message, error) {
	// Read 4-byte length prefix
	var length uint32
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("failed to read message length: %w", err)
	}

	// Keep-alive message (length = 0)
	if length == 0 {
		return &Message{ID: nil, Payload: nil}, nil
	}

	// Read message ID (1 byte)
	var msgID MessageID
	if err := binary.Read(conn, binary.BigEndian, &msgID); err != nil {
		return nil, fmt.Errorf("failed to read message ID: %w", err)
	}

	// Read payload (length - 1 bytes, since we already read the ID)
	payloadLength := length - 1
	payload := make([]byte, payloadLength)
	if payloadLength > 0 {
		if _, err := io.ReadFull(conn, payload); err != nil {
			return nil, fmt.Errorf("failed to read message payload: %w", err)
		}
	}

	return &Message{ID: &msgID, Payload: payload}, nil
}

// SendMessage serializes and sends a message to the connection.
func SendMessage(conn io.Writer, msg *Message) error {
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}
