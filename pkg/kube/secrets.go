package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EnsureSecret creates the Secret if missing or merges the given keys into it
// otherwise. Other keys not in `kvs` are left untouched.
func (c *Client) EnsureSecret(ctx context.Context, ns, name string, kvs map[string]string) error {
	if c == nil {
		return fmt.Errorf("kube client not initialized")
	}
	existing, err := c.cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// Use Data (bytes), not StringData — the apiserver converts StringData→Data
		// server-side, but client-go fakes don't, and downstream reads go through
		// Data. Writing Data directly keeps real + fake behavior identical.
		data := make(map[string][]byte, len(kvs))
		for k, v := range kvs {
			data[k] = []byte(v)
		}
		secret := &corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Type:       corev1.SecretTypeOpaque,
			Data:       data,
		}
		_, err := c.cs.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{FieldManager: FieldManager})
		if err != nil {
			return fmt.Errorf("create secret %s/%s: %w", ns, name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get secret %s/%s: %w", ns, name, err)
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	for k, v := range kvs {
		existing.Data[k] = []byte(v)
	}
	_, err = c.cs.CoreV1().Secrets(ns).Update(ctx, existing, metav1.UpdateOptions{FieldManager: FieldManager})
	if err != nil {
		return fmt.Errorf("update secret %s/%s: %w", ns, name, err)
	}
	return nil
}

// UpsertSecretKey adds or updates a single key in a Secret. Creates the
// Secret if it doesn't exist. Idempotent.
func (c *Client) UpsertSecretKey(ctx context.Context, ns, name, key, value string) error {
	return c.EnsureSecret(ctx, ns, name, map[string]string{key: value})
}

// DeleteSecretKey removes a single key from a Secret. Idempotent — succeeds
// if the Secret or the key is already absent.
func (c *Client) DeleteSecretKey(ctx context.Context, ns, name, key string) error {
	secret, err := c.cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get secret %s/%s: %w", ns, name, err)
	}
	if _, ok := secret.Data[key]; !ok {
		return nil
	}
	delete(secret.Data, key)
	_, err = c.cs.CoreV1().Secrets(ns).Update(ctx, secret, metav1.UpdateOptions{FieldManager: FieldManager})
	if err != nil {
		return fmt.Errorf("delete key %s from secret %s/%s: %w", key, ns, name, err)
	}
	return nil
}

// DeleteSecret removes a Secret entirely. Idempotent.
func (c *Client) DeleteSecret(ctx context.Context, ns, name string) error {
	err := c.cs.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete secret %s/%s: %w", ns, name, err)
	}
	return nil
}

// ListSecretKeys returns the keys of a Secret, or nil if the Secret doesn't exist.
func (c *Client) ListSecretKeys(ctx context.Context, ns, name string) ([]string, error) {
	secret, err := c.cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get secret %s/%s: %w", ns, name, err)
	}
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	return keys, nil
}

// GetSecretValue returns the decoded value of a single key in a Secret.
// secret.Data is already []byte (base64-decoded by the API server).
func (c *Client) GetSecretValue(ctx context.Context, ns, name, key string) (string, error) {
	secret, err := c.cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", fmt.Errorf("secret %s/%s not found", ns, name)
	}
	if err != nil {
		return "", fmt.Errorf("get secret %s/%s: %w", ns, name, err)
	}
	val, ok := secret.Data[key]
	if !ok || len(val) == 0 {
		return "", fmt.Errorf("secret key %q not found or empty", key)
	}
	return string(val), nil
}
