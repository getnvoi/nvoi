package api

import (
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func OpenDB() (*gorm.DB, error) {
	dsn := os.Getenv("MAIN_DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("MAIN_DATABASE_URL is required")
	}

	logLevel := logger.Warn
	if os.Getenv("DEBUG") == "1" {
		logLevel = logger.Info
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if err := db.AutoMigrate(
		&User{},
		&Workspace{},
		&WorkspaceUser{},
		&InfraProvider{},
		&Repo{},
		&CommandLog{},
	); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}

	return db, nil
}
