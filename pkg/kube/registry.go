package kube

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// PullSecretName is the canonical name of the imagePullSecrets-target
// Secret nvoi creates in the app namespace. Single-source so reconcile,
// generate, and tests all agree.
const PullSecretName = "registry-auth"

// RegistryAuth is one host's resolved credentials. Both fields are
// post-resolution literals (the `$VAR` expansion already happened upstream
// in the reconcile layer via CredentialSource).
type RegistryAuth struct {
	Username string
	Password string
}

// dockerConfigJSON is the schema kubelet expects under
// `Secret.Data[".dockerconfigjson"]`. We render it directly rather than
// relying on `docker login` semantics — fewer moving parts, deterministic
// bytes (sorted keys).
type dockerConfigJSON struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

type dockerAuthEntry struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// BuildDockerConfigJSON renders a Docker config JSON for the given resolved
// registries. Output is deterministic — hosts iterated in sorted order so
// re-running with identical input produces byte-identical bytes (matters
// for Apply's "is the Secret different" comparison and for reproducible
// tests).
//
// Per the Docker config spec, `auth` is base64(username:password) in
// addition to the explicit fields — older clients use one, newer ones the
// other. We populate both so the Secret works against any kubelet version.
func BuildDockerConfigJSON(creds map[string]RegistryAuth) ([]byte, error) {
	out := dockerConfigJSON{Auths: make(map[string]dockerAuthEntry, len(creds))}
	hosts := make([]string, 0, len(creds))
	for h := range creds {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		c := creds[h]
		if c.Username == "" {
			return nil, fmt.Errorf("registry %s: username empty after resolution", h)
		}
		if c.Password == "" {
			return nil, fmt.Errorf("registry %s: password empty after resolution", h)
		}
		out.Auths[h] = dockerAuthEntry{
			Username: c.Username,
			Password: c.Password,
			Auth:     base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password)),
		}
	}
	return json.Marshal(out)
}

// BuildPullSecret returns a typed `kubernetes.io/dockerconfigjson` Secret
// in ns named PullSecretName, holding the rendered Docker config. Apply
// it via Client.Apply (the typed *corev1.Secret path already handles
// create-or-update semantics).
func BuildPullSecret(ns string, creds map[string]RegistryAuth) (*corev1.Secret, error) {
	cfgBytes, err := BuildDockerConfigJSON(creds)
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      PullSecretName,
			Namespace: ns,
			Labels: map[string]string{
				utils.LabelAppManagedBy: utils.LabelManagedBy,
			},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: cfgBytes,
		},
	}, nil
}
