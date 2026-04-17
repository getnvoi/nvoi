package kube

import (
	"context"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnsureSecret_CreatesWhenMissing(t *testing.T) {
	c := newTestClient()
	err := c.EnsureSecret(context.Background(), "ns", "app-secrets", map[string]string{
		"DB_PASS": "s3cret",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sec, err := c.cs.CoreV1().Secrets("ns").Get(context.Background(), "app-secrets", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(sec.Data["DB_PASS"]); got != "s3cret" {
		t.Errorf("DB_PASS = %q, want s3cret", got)
	}
}

func TestEnsureSecret_MergesExisting(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "ns"},
		Data:       map[string][]byte{"OLD": []byte("keep")},
	}
	c := newTestClient(existing)

	err := c.EnsureSecret(context.Background(), "ns", "app-secrets", map[string]string{
		"NEW": "added",
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	got, _ := c.cs.CoreV1().Secrets("ns").Get(context.Background(), "app-secrets", metav1.GetOptions{})
	if string(got.Data["OLD"]) != "keep" {
		t.Errorf("OLD lost: %q", got.Data["OLD"])
	}
	if string(got.Data["NEW"]) != "added" {
		t.Errorf("NEW not added: %q", got.Data["NEW"])
	}
}

func TestUpsertSecretKey_RoundTrip(t *testing.T) {
	c := newTestClient()
	if err := c.UpsertSecretKey(context.Background(), "ns", "app-secrets", "KEY", "val"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	val, err := c.GetSecretValue(context.Background(), "ns", "app-secrets", "KEY")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "val" {
		t.Errorf("got %q, want val", val)
	}
}

func TestDeleteSecretKey_Idempotent_MissingSecret(t *testing.T) {
	c := newTestClient()
	if err := c.DeleteSecretKey(context.Background(), "ns", "missing", "KEY"); err != nil {
		t.Errorf("missing secret should be idempotent: %v", err)
	}
}

func TestDeleteSecretKey_Idempotent_MissingKey(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "ns"},
		Data:       map[string][]byte{"OTHER": []byte("x")},
	}
	c := newTestClient(existing)
	if err := c.DeleteSecretKey(context.Background(), "ns", "app-secrets", "MISSING"); err != nil {
		t.Errorf("missing key should be idempotent: %v", err)
	}
}

func TestDeleteSecretKey_RemovesKey(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "ns"},
		Data: map[string][]byte{
			"KEEP": []byte("keep"),
			"DROP": []byte("drop"),
		},
	}
	c := newTestClient(existing)
	if err := c.DeleteSecretKey(context.Background(), "ns", "app-secrets", "DROP"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := c.cs.CoreV1().Secrets("ns").Get(context.Background(), "app-secrets", metav1.GetOptions{})
	if _, present := got.Data["DROP"]; present {
		t.Error("DROP should be removed")
	}
	if string(got.Data["KEEP"]) != "keep" {
		t.Error("KEEP should survive")
	}
}

func TestDeleteSecret_Idempotent(t *testing.T) {
	c := newTestClient()
	if err := c.DeleteSecret(context.Background(), "ns", "missing"); err != nil {
		t.Errorf("missing secret delete should be idempotent: %v", err)
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "here", Namespace: "ns"},
	}
	c = newTestClient(existing)
	if err := c.DeleteSecret(context.Background(), "ns", "here"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestListSecretKeys(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "ns"},
		Data: map[string][]byte{
			"ALPHA": []byte("a"),
			"BETA":  []byte("b"),
		},
	}
	c := newTestClient(existing)

	keys, err := c.ListSecretKeys(context.Background(), "ns", "app-secrets")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "ALPHA" || keys[1] != "BETA" {
		t.Errorf("keys = %v, want [ALPHA BETA]", keys)
	}
}

func TestListSecretKeys_MissingSecret_ReturnsNil(t *testing.T) {
	c := newTestClient()
	keys, err := c.ListSecretKeys(context.Background(), "ns", "missing")
	if err != nil {
		t.Fatalf("missing secret should not error: %v", err)
	}
	if keys != nil {
		t.Errorf("expected nil keys, got %v", keys)
	}
}

func TestGetSecretValue_NotFound_ErrsClearly(t *testing.T) {
	c := newTestClient()
	_, err := c.GetSecretValue(context.Background(), "ns", "missing", "KEY")
	if err == nil || !contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

func TestGetSecretValue_KeyAbsent_ErrsClearly(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "ns"},
		Data:       map[string][]byte{"KEEP": []byte("v")},
	}
	c := newTestClient(existing)
	_, err := c.GetSecretValue(context.Background(), "ns", "app-secrets", "MISSING")
	if err == nil || !contains(err.Error(), "not found") {
		t.Fatalf("expected key-missing error, got: %v", err)
	}
}
