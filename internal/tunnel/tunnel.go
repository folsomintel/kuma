// Package tunnel implements the end-to-end encrypted channel between a kuma
// client and a daemon.
//
// The relay only ever sees opaque frames. Each frame is one kind byte followed
// by an AES-256-GCM blob (see package crypto):
//
//	hello  client -> daemon  static key   client ephemeral X25519 public key
//	ack    daemon -> client  static key   client public key || daemon public key
//	data   either direction  channel key  one protocol.Message
//
// A client opens a channel by sending hello and waiting for the matching ack.
// Both sides then derive two fresh keys, one per direction, from the X25519
// shared secret and the long-lived machine key. Sequence numbers start at zero
// for every channel and only ever move forward.
//
// What that buys:
//   - Frames recorded from an earlier channel do not decrypt on a later one,
//     so they cannot be replayed after either side reconnects.
//   - A frame cannot be reflected back at its sender, because each direction
//     has its own key and associated data.
//   - Channel state belongs to the client/daemon pair rather than to a relay
//     connection, so a new client always starts cleanly.
//   - Recorded traffic stays private if the machine key leaks later.
//
// What it does not do: whoever carries the frames (the relay) can still drop
// them, and can replay an old hello to make the daemon abandon the current
// channel. Both only deny service; neither reveals nor forges messages.
package tunnel

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/folsomintel/kuma/internal/crypto"
	"github.com/folsomintel/kuma/internal/protocol"
)

// Frame kinds, carried in the first byte of every frame.
const (
	KindHello byte = 1
	KindAck   byte = 2
	KindData  byte = 3
)

const (
	label      = "kuma-tunnel-v1"
	pubKeySize = 32
)

var (
	// ErrMalformed means a frame is not of the kind or shape expected.
	ErrMalformed = errors.New("tunnel: malformed frame")
	// ErrReplay means a data frame's sequence number did not move forward.
	ErrReplay = errors.New("tunnel: replayed or out-of-order frame")
	// ErrAckMismatch means an ack answers some other handshake.
	ErrAckMismatch = errors.New("tunnel: ack does not match this handshake")
)

// FrameKind returns the kind byte of frame, or 0 when frame is empty.
func FrameKind(frame []byte) byte {
	if len(frame) == 0 {
		return 0
	}
	return frame[0]
}

// Channel is an established tunnel in one peer's hands.
//
// Seal must not be called concurrently with itself, and frames must be written
// to the wire in the order Seal returned them. Open must not be called
// concurrently with itself. Seal and Open may run concurrently with each other.
type Channel struct {
	send, recv       *crypto.Cipher
	sendAAD, recvAAD []byte
	sendSeq, recvSeq uint64
}

// Seal assigns the next sequence number to msg and returns its data frame.
func (c *Channel) Seal(msg protocol.Message) ([]byte, error) {
	msg.Seq = c.sendSeq + 1
	plain, err := protocol.Encode(msg)
	if err != nil {
		return nil, err
	}
	frame, err := sealFrame(c.send, KindData, plain, c.sendAAD)
	if err != nil {
		return nil, err
	}
	c.sendSeq = msg.Seq
	return frame, nil
}

// Open decrypts a data frame and rejects any whose sequence number is not
// greater than the last one accepted.
func (c *Channel) Open(frame []byte) (protocol.Message, error) {
	plain, err := openFrame(c.recv, KindData, frame, c.recvAAD)
	if err != nil {
		return protocol.Message{}, err
	}
	msg, err := protocol.Decode(plain)
	if err != nil {
		return protocol.Message{}, err
	}
	if msg.Seq <= c.recvSeq {
		return protocol.Message{}, ErrReplay
	}
	c.recvSeq = msg.Seq
	return msg, nil
}

// Initiator is the client half of a handshake.
type Initiator struct {
	peer
	priv  *ecdh.PrivateKey
	hello []byte
}

// NewInitiator prepares a handshake for the machine identified by machineID.
func NewInitiator(key []byte, machineID string) (*Initiator, error) {
	p, err := newPeer(key, machineID)
	if err != nil {
		return nil, err
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	hello, err := sealFrame(p.static, KindHello, priv.PublicKey().Bytes(), p.aad("hello"))
	if err != nil {
		return nil, err
	}
	return &Initiator{peer: p, priv: priv, hello: hello}, nil
}

// Hello returns the hello frame. It may be sent more than once; the daemon
// answers every copy with the same ack.
func (i *Initiator) Hello() []byte {
	return i.hello
}

// Finish consumes the daemon's ack and returns the client side of the channel.
func (i *Initiator) Finish(ack []byte) (*Channel, error) {
	plain, err := openFrame(i.static, KindAck, ack, i.aad("ack"))
	if err != nil {
		return nil, err
	}
	if len(plain) != 2*pubKeySize {
		return nil, ErrMalformed
	}
	clientPub, daemonPub := plain[:pubKeySize], plain[pubKeySize:]
	if !bytes.Equal(clientPub, i.priv.PublicKey().Bytes()) {
		return nil, ErrAckMismatch
	}
	c2d, d2c, err := i.channelCiphers(i.priv, clientPub, daemonPub, daemonPub)
	if err != nil {
		return nil, err
	}
	return &Channel{
		send: c2d, sendAAD: i.aad("data|c2d"),
		recv: d2c, recvAAD: i.aad("data|d2c"),
	}, nil
}

// Responder is the daemon half of the handshake. It is not safe for
// concurrent use.
type Responder struct {
	peer

	// The most recent handshake, so a retransmitted hello gets the same ack
	// back instead of tearing down the channel it already created.
	lastClientPub []byte
	lastAck       []byte
	lastChannel   *Channel
}

// NewResponder prepares to accept handshakes for machineID.
func NewResponder(key []byte, machineID string) (*Responder, error) {
	p, err := newPeer(key, machineID)
	if err != nil {
		return nil, err
	}
	return &Responder{peer: p}, nil
}

// Accept consumes a hello and returns the daemon side of the channel plus the
// ack to send back. A hello repeating the most recent handshake returns that
// handshake's channel and ack unchanged; any other hello starts a new channel.
func (r *Responder) Accept(hello []byte) (*Channel, []byte, error) {
	clientPub, err := openFrame(r.static, KindHello, hello, r.aad("hello"))
	if err != nil {
		return nil, nil, err
	}
	if len(clientPub) != pubKeySize {
		return nil, nil, ErrMalformed
	}
	if r.lastChannel != nil && bytes.Equal(clientPub, r.lastClientPub) {
		return r.lastChannel, r.lastAck, nil
	}

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	daemonPub := priv.PublicKey().Bytes()
	c2d, d2c, err := r.channelCiphers(priv, clientPub, daemonPub, clientPub)
	if err != nil {
		return nil, nil, err
	}
	ack, err := sealFrame(r.static, KindAck, append(bytes.Clone(clientPub), daemonPub...), r.aad("ack"))
	if err != nil {
		return nil, nil, err
	}
	ch := &Channel{
		send: d2c, sendAAD: r.aad("data|d2c"),
		recv: c2d, recvAAD: r.aad("data|c2d"),
	}
	r.lastClientPub, r.lastAck, r.lastChannel = clientPub, ack, ch
	return ch, ack, nil
}

// peer holds what both handshake roles share.
type peer struct {
	static    *crypto.Cipher
	staticKey []byte
	machineID string
}

func newPeer(key []byte, machineID string) (peer, error) {
	if machineID == "" {
		return peer{}, errors.New("tunnel: machine id is required")
	}
	static, err := crypto.NewCipher(key)
	if err != nil {
		return peer{}, err
	}
	return peer{static: static, staticKey: bytes.Clone(key), machineID: machineID}, nil
}

// aad binds a frame to this protocol version, its purpose, and the machine.
func (p peer) aad(purpose string) []byte {
	return []byte(label + "|" + purpose + "|" + p.machineID)
}

// channelCiphers derives the two per-direction ciphers. remotePub is the other
// side's public key; clientPub and daemonPub pin both keys into the result so
// the two peers only agree when they saw the same handshake.
func (p peer) channelCiphers(priv *ecdh.PrivateKey, clientPub, daemonPub, remotePub []byte) (c2d, d2c *crypto.Cipher, err error) {
	remote, err := ecdh.X25519().NewPublicKey(remotePub)
	if err != nil {
		return nil, nil, fmt.Errorf("tunnel: peer public key: %w", err)
	}
	shared, err := priv.ECDH(remote)
	if err != nil {
		return nil, nil, fmt.Errorf("tunnel: key agreement: %w", err)
	}
	// Mixing in the machine key means the channel keys are only reachable by
	// someone who holds it, even if they could tamper with the exchange.
	prk, err := hkdf.Extract(sha256.New, shared, p.staticKey)
	if err != nil {
		return nil, nil, err
	}
	transcript := string(clientPub) + string(daemonPub) + p.machineID
	derive := func(direction string) (*crypto.Cipher, error) {
		key, err := hkdf.Expand(sha256.New, prk, label+"|"+direction+"|"+transcript, crypto.KeySize)
		if err != nil {
			return nil, err
		}
		return crypto.NewCipher(key)
	}
	if c2d, err = derive("c2d"); err != nil {
		return nil, nil, err
	}
	if d2c, err = derive("d2c"); err != nil {
		return nil, nil, err
	}
	return c2d, d2c, nil
}

func sealFrame(c *crypto.Cipher, kind byte, plaintext, aad []byte) ([]byte, error) {
	blob, err := c.Encrypt(plaintext, aad)
	if err != nil {
		return nil, err
	}
	return append([]byte{kind}, blob...), nil
}

func openFrame(c *crypto.Cipher, kind byte, frame, aad []byte) ([]byte, error) {
	if FrameKind(frame) != kind {
		return nil, ErrMalformed
	}
	return c.Decrypt(frame[1:], aad)
}
