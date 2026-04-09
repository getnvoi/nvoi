package commands

import "testing"

func TestSecretSet(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewSecretCmd(m), "set", "KEY", "VALUE")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "SecretSet")
	assertArg(t, m, 0, "KEY")
	assertArg(t, m, 1, "VALUE")
}

func TestSecretSet_MissingArgs(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewSecretCmd(m), "set", "KEY")
	assertError(t, err, "")
}

func TestSecretDelete(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewSecretCmd(m), "delete", "KEY")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "SecretDelete")
	assertArg(t, m, 0, "KEY")
}

func TestSecretList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewSecretCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "SecretList")
}

func TestSecretReveal(t *testing.T) {
	m := &MockBackend{RevealValue: "s3cret"}
	err := runCmd(t, NewSecretCmd(m), "reveal", "KEY")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "SecretReveal")
	assertArg(t, m, 0, "KEY")
}
