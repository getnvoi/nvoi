// Package main runs a minimal HTTP server that records every GET / as a row
// in a Postgres "log" table (single jsonb data column). Used as the dogfood
// deploy target for nvoi — api.nvoi.to.
//
// Configuration (env):
//   - DATABASE_URL (required): postgres DSN
//   - PORT (optional, default 8080)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Log is one row per hit. The jsonb column lets the shape evolve without
// schema migrations — we just write whatever dict the handler builds.
type Log struct {
	ID        uint           `gorm:"primaryKey"`
	Data      datatypes.JSON `gorm:"type:jsonb;not null"`
	CreatedAt time.Time
}

// TableName pins the table to `log` — without this, GORM would pluralize.
func (Log) TableName() string { return "log" }

// HitInput captures request metadata we want to persist.
type HitInput struct {
	UserAgent string `header:"User-Agent"`
}

// HitOutput is the JSON body returned after a hit is recorded.
type HitOutput struct {
	Body struct {
		ID        uint      `json:"id"`
		CreatedAt time.Time `json:"created_at"`
	}
}

func main() {
	dsn := mustEnv("DATABASE_URL")

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	// Postgres may not be ready when the pod starts (rolling deploy). Retry
	// the first real query (AutoMigrate) for up to a minute before giving up.
	if err := waitForDB(db, 60*time.Second); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := humagin.New(r, huma.DefaultConfig("nvoi api", "0.1.0"))

	huma.Register(api, huma.Operation{
		OperationID: "hit-root",
		Method:      http.MethodGet,
		Path:        "/",
		Summary:     "Record a hit and return its id",
	}, hit(db))

	addr := ":" + envOr("PORT", "8080")
	log.Printf("listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// hit returns the GET / handler, closing over db.
func hit(db *gorm.DB) func(ctx context.Context, in *HitInput) (*HitOutput, error) {
	return func(ctx context.Context, in *HitInput) (*HitOutput, error) {
		payload, err := json.Marshal(map[string]any{
			"user_agent": in.UserAgent,
			"at":         time.Now().UTC(),
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(fmt.Sprintf("marshal: %v", err))
		}
		entry := Log{Data: datatypes.JSON(payload)}
		if err := db.WithContext(ctx).Create(&entry).Error; err != nil {
			return nil, huma.Error500InternalServerError(fmt.Sprintf("insert: %v", err))
		}
		out := &HitOutput{}
		out.Body.ID = entry.ID
		out.Body.CreatedAt = entry.CreatedAt
		return out, nil
	}
}

// waitForDB retries AutoMigrate until it succeeds or timeout elapses.
func waitForDB(db *gorm.DB, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := db.AutoMigrate(&Log{})
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		log.Printf("db not ready (%v), retrying…", err)
		time.Sleep(2 * time.Second)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}
