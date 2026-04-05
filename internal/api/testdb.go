package api

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestDB returns an in-memory SQLite database with all tables migrated.
// For use in tests only — no PostgreSQL required.
func TestDB() *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		panic("open test db: " + err.Error())
	}
	if err := db.AutoMigrate(
		&User{},
		&Workspace{},
		&WorkspaceUser{},
		&Repo{},
		&RepoConfig{},
	); err != nil {
		panic("migrate test db: " + err.Error())
	}
	return db
}
