package jointoken

import "testing"

func TestMintAndValid(t *testing.T) {
	secret := "test-secret"
	tok, err := Mint(secret, "m1", RoleDaemon)
	if err != nil {
		t.Fatal(err)
	}
	if !Valid(secret, "m1", RoleDaemon, tok) {
		t.Fatal("expected valid")
	}
	if Valid(secret, "m1", RoleClient, tok) {
		t.Fatal("daemon token must not validate as client")
	}
	if Valid(secret, "m2", RoleDaemon, tok) {
		t.Fatal("token must not validate for other machine")
	}
	if Valid("other", "m1", RoleDaemon, tok) {
		t.Fatal("token must not validate for other secret")
	}
}

func TestMintRequiresInputs(t *testing.T) {
	if _, err := Mint("", "m", RoleDaemon); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Mint("s", "", RoleDaemon); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Mint("s", "m", "nope"); err == nil {
		t.Fatal("expected error")
	}
}
