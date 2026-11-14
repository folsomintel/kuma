package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncodeDecodeRequest(t *testing.T) {
	msg := Message{
		Type:   TypeRequest,
		Seq:    1,
		ID:     "req-1",
		Method: MethodListAgents,
	}

	raw, err := Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Type != TypeRequest || got.ID != "req-1" || got.Method != MethodListAgents || got.Seq != 1 {
		t.Fatalf("unexpected message: %+v", got)
	}
}

func TestValidateRejectsBadMessages(t *testing.T) {
	cases := []Message{
		{Type: TypeRequest, ID: "1", Method: MethodListAgents},
		{Type: TypeRequest, Seq: 1, Method: MethodListAgents},
		{Type: TypeRequest, Seq: 1, ID: "1"},
		{Type: TypeRequest, Seq: 1, ID: "1", Method: "nope"},
		{Type: TypeResponse, Seq: 1, ID: "1"},
		{Type: TypeInput, Seq: 1},
		{Type: TypeResize, Seq: 1, SessionID: "s1", Rows: 0, Cols: 80},
		{Type: Type("wat"), Seq: 1},
	}

	for i, msg := range cases {
		if err := Validate(msg); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, msg)
		}
	}
}

func TestStartSessionParamsRoundTrip(t *testing.T) {
	params := StartSessionParams{Agent: "claude", CWD: "/tmp"}
	encoded, err := EncodeParams(params)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}
	msg := Message{
		Type:   TypeRequest,
		Seq:    2,
		ID:     "2",
		Method: MethodStartSession,
		Params: encoded,
	}

	raw, err := Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	decoded, err := DecodeParams[StartSessionParams](got.Params)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	if decoded != params {
		t.Fatalf("got %+v want %+v", decoded, params)
	}
}

func TestOutputDataRoundTrip(t *testing.T) {
	payload := []byte("hello\x00world")
	msg := Message{
		Type:      TypeOutput,
		Seq:       3,
		SessionID: "sess-1",
		Data:      payload,
	}
	raw, err := Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got.Data, payload) {
		t.Fatalf("data mismatch")
	}

	var check map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := check["v"]; ok {
		t.Fatal("unexpected version field in encoded message")
	}
}
