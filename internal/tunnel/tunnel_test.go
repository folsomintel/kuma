package tunnel

import (
	"errors"
	"testing"

	"github.com/folsomintel/kuma/internal/crypto"
	"github.com/folsomintel/kuma/internal/protocol"
)

const testMachine = "machine-1"

func newKey(t *testing.T) []byte {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// handshake runs a full exchange and returns both ends plus the frames that
// crossed the wire.
func handshake(t *testing.T, key []byte, r *Responder) (client, daemon *Channel, hello, ack []byte) {
	t.Helper()
	i, err := NewInitiator(key, testMachine)
	if err != nil {
		t.Fatal(err)
	}
	hello = i.Hello()
	daemon, ack, err = r.Accept(hello)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	client, err = i.Finish(ack)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return client, daemon, hello, ack
}

func newResponder(t *testing.T, key []byte) *Responder {
	t.Helper()
	r, err := NewResponder(key, testMachine)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func request(id string) protocol.Message {
	return protocol.Message{Type: protocol.TypeRequest, ID: id, Method: protocol.MethodListSessions}
}

func TestRoundTripBothDirections(t *testing.T) {
	key := newKey(t)
	client, daemon, _, _ := handshake(t, key, newResponder(t, key))

	frame, err := client.Seal(request("a"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := daemon.Open(frame)
	if err != nil {
		t.Fatalf("daemon Open: %v", err)
	}
	if got.ID != "a" || got.Seq != 1 {
		t.Fatalf("got id=%q seq=%d", got.ID, got.Seq)
	}

	reply, err := daemon.Seal(protocol.Message{Type: protocol.TypeResponse, ID: "a", OK: protocol.BoolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Open(reply); err != nil {
		t.Fatalf("client Open: %v", err)
	}
}

func TestReplayOnSameChannelRejected(t *testing.T) {
	key := newKey(t)
	client, daemon, _, _ := handshake(t, key, newResponder(t, key))

	frame, _ := client.Seal(request("a"))
	if _, err := daemon.Open(frame); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.Open(frame); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay: got %v, want ErrReplay", err)
	}
}

func TestOlderFrameRejectedAfterNewerOne(t *testing.T) {
	key := newKey(t)
	client, daemon, _, _ := handshake(t, key, newResponder(t, key))

	first, _ := client.Seal(request("1"))
	second, _ := client.Seal(request("2"))
	if _, err := daemon.Open(second); err != nil {
		t.Fatalf("a gap must be tolerated: %v", err)
	}
	if _, err := daemon.Open(first); !errors.Is(err, ErrReplay) {
		t.Fatalf("stale frame: got %v, want ErrReplay", err)
	}
}

// A long run of frames the receiver never saw (for example output produced
// while the relay had nobody to deliver to) must not wedge the channel.
func TestLargeGapAccepted(t *testing.T) {
	key := newKey(t)
	client, daemon, _, _ := handshake(t, key, newResponder(t, key))

	var last []byte
	for range 500 {
		last, _ = client.Seal(request("x"))
	}
	if _, err := daemon.Open(last); err != nil {
		t.Fatalf("Open after gap: %v", err)
	}
}

func TestFrameFromEarlierChannelRejected(t *testing.T) {
	key := newKey(t)
	r := newResponder(t, key)

	oldClient, oldDaemon, _, _ := handshake(t, key, r)
	captured, _ := oldClient.Seal(request("captured"))
	if _, err := oldDaemon.Open(captured); err != nil {
		t.Fatal(err)
	}

	_, newDaemon, _, _ := handshake(t, key, r)
	if _, err := newDaemon.Open(captured); err == nil {
		t.Fatal("frame from an earlier channel was accepted")
	}
}

// Replaying a recorded hello makes the daemon start a channel, but nobody
// without the original ephemeral key can use it, and old data stays dead.
func TestReplayedHelloDoesNotReviveOldFrames(t *testing.T) {
	key := newKey(t)
	r := newResponder(t, key)

	oldClient, _, oldHello, oldAck := handshake(t, key, r)
	captured, _ := oldClient.Seal(request("captured"))
	handshake(t, key, r) // another client comes and goes

	replayed, ack, err := r.Accept(oldHello)
	if err != nil {
		t.Fatalf("Accept replayed hello: %v", err)
	}
	if string(ack) == string(oldAck) {
		t.Fatal("replayed hello got the original ack, so the original keys are back in use")
	}
	if _, err := replayed.Open(captured); err == nil {
		t.Fatal("frame from the original channel was accepted after a hello replay")
	}
}

func TestRetransmittedHelloKeepsChannel(t *testing.T) {
	key := newKey(t)
	r := newResponder(t, key)
	i, _ := NewInitiator(key, testMachine)

	first, ack1, err := r.Accept(i.Hello())
	if err != nil {
		t.Fatal(err)
	}
	second, ack2, err := r.Accept(i.Hello())
	if err != nil {
		t.Fatal(err)
	}
	if first != second || string(ack1) != string(ack2) {
		t.Fatal("retransmitted hello replaced the channel")
	}
}

func TestFrameCannotBeReflected(t *testing.T) {
	key := newKey(t)
	client, daemon, _, _ := handshake(t, key, newResponder(t, key))

	fromClient, _ := client.Seal(request("a"))
	if _, err := client.Open(fromClient); err == nil {
		t.Fatal("client accepted its own frame")
	}
	fromDaemon, _ := daemon.Seal(protocol.Message{Type: protocol.TypeResponse, ID: "a", OK: protocol.BoolPtr(true)})
	if _, err := daemon.Open(fromDaemon); err == nil {
		t.Fatal("daemon accepted its own frame")
	}
}

func TestAckForAnotherHandshakeRejected(t *testing.T) {
	key := newKey(t)
	r := newResponder(t, key)
	_, _, _, otherAck := handshake(t, key, r)

	i, _ := NewInitiator(key, testMachine)
	if _, err := i.Finish(otherAck); !errors.Is(err, ErrAckMismatch) {
		t.Fatalf("got %v, want ErrAckMismatch", err)
	}
}

func TestWrongKeyOrMachineRejected(t *testing.T) {
	key := newKey(t)
	i, _ := NewInitiator(key, testMachine)

	if _, _, err := newResponder(t, newKey(t)).Accept(i.Hello()); err == nil {
		t.Fatal("hello accepted under a different key")
	}
	other, err := NewResponder(key, "machine-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.Accept(i.Hello()); err == nil {
		t.Fatal("hello accepted for a different machine")
	}
}

func TestFrameKindsAreNotInterchangeable(t *testing.T) {
	key := newKey(t)
	r := newResponder(t, key)
	client, daemon, hello, ack := handshake(t, key, r)
	data, _ := client.Seal(request("a"))

	relabel := func(frame []byte, kind byte) []byte {
		out := append([]byte(nil), frame...)
		out[0] = kind
		return out
	}

	if _, _, err := r.Accept(relabel(ack, KindHello)); err == nil {
		t.Fatal("ack accepted as hello")
	}
	if _, _, err := r.Accept(data); !errors.Is(err, ErrMalformed) {
		t.Fatalf("data as hello: got %v, want ErrMalformed", err)
	}
	if _, err := daemon.Open(relabel(hello, KindData)); err == nil {
		t.Fatal("hello accepted as data")
	}
	if _, err := daemon.Open(nil); !errors.Is(err, ErrMalformed) {
		t.Fatalf("empty frame: got %v, want ErrMalformed", err)
	}
}

func TestTamperedDataRejected(t *testing.T) {
	key := newKey(t)
	client, daemon, _, _ := handshake(t, key, newResponder(t, key))

	frame, _ := client.Seal(request("a"))
	frame[len(frame)-1] ^= 0x01
	if _, err := daemon.Open(frame); err == nil {
		t.Fatal("tampered frame accepted")
	}
}
