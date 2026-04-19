package ngrok

import (
	"github.com/getnvoi/nvoi/pkg/provider"
)

var Schema = provider.CredentialSchema{
	Name: "ngrok tunnel",
	Fields: []provider.CredentialField{
		{Key: "api_key", Required: true, EnvVar: "NGROK_API_KEY", Flag: "ngrok-api-key"},
		{Key: "authtoken", Required: true, EnvVar: "NGROK_AUTHTOKEN", Flag: "ngrok-authtoken"},
	},
}

func init() {
	provider.RegisterTunnel("ngrok", Schema, func(creds map[string]string) provider.TunnelProvider {
		return NewClient(creds)
	})
}
