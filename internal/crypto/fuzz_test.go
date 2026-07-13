package crypto

import "testing"

func FuzzEncryptDecrypt(f *testing.F) {
	key, err := GenerateKey()
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte("hello"), []byte("aad"))
	f.Add([]byte{}, []byte(""))
	f.Add([]byte{0, 1, 2, 3}, []byte("machine"))

	f.Fuzz(func(t *testing.T, plaintext, aad []byte) {
		blob, err := Encrypt(key, plaintext, aad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decrypt(key, blob, aad)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(plaintext) {
			t.Fatalf("round-trip mismatch")
		}
		if _, err := Decrypt(key, blob, append(aad, 'x')); err == nil {
			t.Fatal("expected AAD mismatch to fail")
		}
	})
}
