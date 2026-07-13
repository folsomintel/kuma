package protocol

import "testing"

func FuzzDecode(f *testing.F) {
	valid, err := Encode(Message{
		Type:   TypeRequest,
		Seq:    1,
		ID:     "1",
		Method: MethodListAgents,
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"type":"request","seq":1,"id":"x","method":"list_agents"}`))
	f.Add([]byte{0xff, 0x00, '{'})

	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := Decode(data)
		if err != nil {
			return
		}
		if _, err := Encode(msg); err != nil {
			t.Fatalf("re-encode valid message: %v", err)
		}
	})
}
