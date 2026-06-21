package password

import "testing"

func TestHashVerify(t *testing.T) {
	hash, err := Hash("client-demo")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify("client-demo", hash) {
		t.Fatal("expected password to verify")
	}
	if Verify("wrong", hash) {
		t.Fatal("wrong password verified")
	}
}

func TestGenerate(t *testing.T) {
	got, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 20 {
		t.Fatalf("generated password too short: %q", got)
	}
}
