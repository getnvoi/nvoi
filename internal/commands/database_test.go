package commands

import (
	"fmt"
	"testing"
)

func TestDatabaseSet_ParsesFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "set", "db",
		"--type", "postgres", "--secret", "KEY",
		"--backup-storage", "s", "--backup-cron", "0 2 * * *",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "DatabaseSet")
	assertArg(t, m, 0, "db")
	opts := m.last().Args[1].(ManagedOpts)
	if opts.Kind != "postgres" {
		t.Fatalf("kind = %q", opts.Kind)
	}
	if opts.BackupStorage != "s" {
		t.Fatalf("backup_storage = %q", opts.BackupStorage)
	}
	if opts.BackupCron != "0 2 * * *" {
		t.Fatalf("backup_cron = %q", opts.BackupCron)
	}
}

func TestDatabaseSet_MissingType(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "set", "db")
	assertError(t, err, "Available database types")
}

func TestDatabaseSet_CustomImage(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "set", "db", "--type", "postgres", "--image", "postgres:16")
	if err != nil {
		t.Fatal(err)
	}
	opts := m.last().Args[1].(ManagedOpts)
	if opts.Image != "postgres:16" {
		t.Fatalf("image = %q", opts.Image)
	}
}

func TestDatabaseSet_VolumeSize(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "set", "db", "--type", "postgres", "--volume-size", "50")
	if err != nil {
		t.Fatal(err)
	}
	opts := m.last().Args[1].(ManagedOpts)
	if opts.VolumeSize != 50 {
		t.Fatalf("volume_size = %d", opts.VolumeSize)
	}
}

func TestDatabaseDelete_ParsesFlags(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "delete", "db", "--type", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "DatabaseDelete")
	assertArg(t, m, 0, "db")
	assertArg(t, m, 1, "postgres")
}

func TestDatabaseDelete_MissingType(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "delete", "db")
	assertError(t, err, "Available database types")
}

func TestDatabaseList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "list")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "DatabaseList")
}

func TestBackupCreate(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "backup", "create", "db", "--type", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "BackupCreate")
	assertArg(t, m, 0, "db")
	assertArg(t, m, 1, "postgres")
}

func TestBackupList(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "backup", "list", "db", "--type", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "BackupList")
	assertArg(t, m, 0, "db")
}

func TestBackupDownload(t *testing.T) {
	m := &MockBackend{}
	err := runCmd(t, NewDatabaseCmd(m), "backup", "download", "db", "key.sql.gz", "--type", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(t, m, "BackupDownload")
	assertArg(t, m, 0, "db")
	assertArg(t, m, 3, "key.sql.gz")
}

func TestDatabaseSet_PropagatesError(t *testing.T) {
	m := &MockBackend{Err: fmt.Errorf("backend failure")}
	err := runCmd(t, NewDatabaseCmd(m), "set", "db", "--type", "postgres")
	assertError(t, err, "backend failure")
}
