package database

import (
	"testing"
)

func TestEngineFor_Postgres(t *testing.T) {
	e := EngineFor("postgres")
	if e.Name() != "postgres" {
		t.Errorf("got %q, want postgres", e.Name())
	}
}

func TestEngineFor_MySQL(t *testing.T) {
	e := EngineFor("mysql")
	if e.Name() != "mysql" {
		t.Errorf("got %q, want mysql", e.Name())
	}
}

func TestEngineFor_UnknownPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for unknown kind")
		}
	}()
	EngineFor("redis")
}

func TestPostgres_ConnectionURL(t *testing.T) {
	p := &Postgres{}
	url := p.ConnectionURL("main-db", 5432, "user", "pass", "mydb")
	want := "postgresql://user:pass@main-db:5432/mydb"
	if url != want {
		t.Errorf("got %q, want %q", url, want)
	}
}

func TestMySQL_ConnectionURL(t *testing.T) {
	m := &MySQL{}
	url := m.ConnectionURL("main-db", 3306, "user", "pass", "mydb")
	want := "mysql://user:pass@main-db:3306/mydb"
	if url != want {
		t.Errorf("got %q, want %q", url, want)
	}
}

func TestPostgres_Port(t *testing.T) {
	if p := (&Postgres{}).Port(); p != 5432 {
		t.Errorf("got %d, want 5432", p)
	}
}

func TestMySQL_Port(t *testing.T) {
	if p := (&MySQL{}).Port(); p != 3306 {
		t.Errorf("got %d, want 3306", p)
	}
}

func TestPostgres_EnvVarNames(t *testing.T) {
	u, p, d := (&Postgres{}).EnvVarNames()
	if u != "POSTGRES_USER" || p != "POSTGRES_PASSWORD" || d != "POSTGRES_DB" {
		t.Errorf("got %s/%s/%s", u, p, d)
	}
}

func TestMySQL_EnvVarNames(t *testing.T) {
	u, p, d := (&MySQL{}).EnvVarNames()
	if u != "MYSQL_USER" || p != "MYSQL_PASSWORD" || d != "MYSQL_DATABASE" {
		t.Errorf("got %s/%s/%s", u, p, d)
	}
}

func TestPostgres_DumpCommand(t *testing.T) {
	cmd := (&Postgres{}).DumpCommand("db-host", "myuser", "mydb")
	if cmd != "pg_dump -h db-host -U myuser -d mydb --no-owner --no-acl" {
		t.Errorf("got %q", cmd)
	}
}

func TestMySQL_DumpCommand(t *testing.T) {
	cmd := (&MySQL{}).DumpCommand("db-host", "myuser", "mydb")
	if cmd != "mysqldump -h db-host -u myuser mydb --single-transaction --routines --triggers" {
		t.Errorf("got %q", cmd)
	}
}

func TestPostgres_PasswordEnvVar(t *testing.T) {
	if v := (&Postgres{}).PasswordEnvVar(); v != "PGPASSWORD" {
		t.Errorf("got %q", v)
	}
}

func TestMySQL_PasswordEnvVar(t *testing.T) {
	if v := (&MySQL{}).PasswordEnvVar(); v != "MYSQL_PWD" {
		t.Errorf("got %q", v)
	}
}

func TestPostgres_ReadinessProbe(t *testing.T) {
	probe := (&Postgres{}).ReadinessProbe("myuser")
	if len(probe) != 3 || probe[0] != "pg_isready" {
		t.Errorf("got %v", probe)
	}
}

func TestMySQL_ReadinessProbe(t *testing.T) {
	probe := (&MySQL{}).ReadinessProbe("myuser")
	if len(probe) != 4 || probe[0] != "mysqladmin" {
		t.Errorf("got %v", probe)
	}
}
