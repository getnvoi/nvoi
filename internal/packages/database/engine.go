package database

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Engine abstracts database-specific behavior. Each database engine
// (postgres, mysql) implements this to provide the correct commands,
// ports, URL format, env vars, and backup/restore scripts.
type Engine interface {
	// Name returns the engine identifier (e.g., "postgres", "mysql").
	Name() string

	// Port returns the default listening port.
	Port() int32

	// ConnectionURL builds the connection string.
	ConnectionURL(host string, port int32, user, password, dbname string) string

	// ContainerEnv returns the env vars the database container needs.
	ContainerEnv(secretName, envPrefix string) []corev1.EnvVar

	// DataDir returns the PGDATA-style env var name and path, if any.
	DataDir() (envName, path string, needed bool)

	// ReadinessProbe returns the command to check if the database is ready.
	ReadinessProbe(user string) []string

	// DumpCommand returns the command to dump the database to stdout.
	DumpCommand(host, user, dbname string) string

	// PasswordEnvVar returns the env var name for the password in dump commands.
	PasswordEnvVar() string

	// EnvVarNames returns the env var suffixes for user, password, database.
	// e.g., postgres: ("POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB")
	// e.g., mysql: ("MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE")
	EnvVarNames() (user, password, database string)
}

// DetectEngine returns the engine based on the container image name.
func DetectEngine(image string) (Engine, error) {
	lower := strings.ToLower(image)
	if strings.Contains(lower, "postgres") || strings.Contains(lower, "pgvector") {
		return &Postgres{}, nil
	}
	if strings.Contains(lower, "mysql") || strings.Contains(lower, "mariadb") {
		return &MySQL{}, nil
	}
	return nil, fmt.Errorf("unsupported database image %q — must contain postgres, mysql, or mariadb", image)
}

// ── Postgres ──────────────────────────────────────────────────────────────────

type Postgres struct{}

func (p *Postgres) Name() string { return "postgres" }
func (p *Postgres) Port() int32  { return 5432 }

func (p *Postgres) ConnectionURL(host string, port int32, user, password, dbname string) string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", user, password, host, port, dbname)
}

func (p *Postgres) ContainerEnv(secretName, envPrefix string) []corev1.EnvVar {
	return []corev1.EnvVar{
		secretEnvVar("POSTGRES_USER", secretName, envPrefix+"_POSTGRES_USER"),
		secretEnvVar("POSTGRES_PASSWORD", secretName, envPrefix+"_POSTGRES_PASSWORD"),
		secretEnvVar("POSTGRES_DB", secretName, envPrefix+"_POSTGRES_DB"),
	}
}

func (p *Postgres) DataDir() (string, string, bool) {
	return "PGDATA", "/var/lib/postgresql/data/pgdata", true
}

func (p *Postgres) ReadinessProbe(user string) []string {
	return []string{"pg_isready", "-U", user}
}

func (p *Postgres) DumpCommand(host, user, dbname string) string {
	return fmt.Sprintf("pg_dump -h %s -U %s -d %s --no-owner --no-acl", host, user, dbname)
}

func (p *Postgres) PasswordEnvVar() string { return "PGPASSWORD" }
func (p *Postgres) EnvVarNames() (string, string, string) {
	return "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"
}

// ── MySQL ─────────────────────────────────────────────────────────────────────

type MySQL struct{}

func (m *MySQL) Name() string { return "mysql" }
func (m *MySQL) Port() int32  { return 3306 }

func (m *MySQL) ConnectionURL(host string, port int32, user, password, dbname string) string {
	return fmt.Sprintf("mysql://%s:%s@%s:%d/%s", user, password, host, port, dbname)
}

func (m *MySQL) ContainerEnv(secretName, envPrefix string) []corev1.EnvVar {
	return []corev1.EnvVar{
		secretEnvVar("MYSQL_USER", secretName, envPrefix+"_MYSQL_USER"),
		secretEnvVar("MYSQL_PASSWORD", secretName, envPrefix+"_MYSQL_PASSWORD"),
		secretEnvVar("MYSQL_DATABASE", secretName, envPrefix+"_MYSQL_DATABASE"),
		secretEnvVar("MYSQL_ROOT_PASSWORD", secretName, envPrefix+"_MYSQL_PASSWORD"),
	}
}

func (m *MySQL) DataDir() (string, string, bool) {
	return "", "/var/lib/mysql", false // MySQL doesn't need a DATADIR env var
}

func (m *MySQL) ReadinessProbe(user string) []string {
	return []string{"mysqladmin", "ping", "-u", user}
}

func (m *MySQL) DumpCommand(host, user, dbname string) string {
	return fmt.Sprintf("mysqldump -h %s -u %s %s --single-transaction --routines --triggers", host, user, dbname)
}

func (m *MySQL) PasswordEnvVar() string { return "MYSQL_PWD" }
func (m *MySQL) EnvVarNames() (string, string, string) {
	return "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE"
}

// helper shared with manifests.go
func secretEnvVar(envName, secretName, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: envName,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		},
	}
}
