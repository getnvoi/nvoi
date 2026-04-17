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

	// Postgres may not exist yet when this pod starts (k8s schedules all
	// workloads in parallel; DNS for the postgres Service may not resolve
	// until its pod is registered; the DB itself may still be initializing
	// volume/user/db). Retry both Open and AutoMigrate for up to 5 minutes.
	db, err := waitForDB(dsn, 5*time.Minute)
	if err != nil {
		log.Fatalf("db not ready: %v", err)
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

// waitForDB retries Open+AutoMigrate until both succeed or the deadline
// elapses. Covers: DNS-not-yet-resolvable, postgres-still-booting, user/db
// not yet provisioned. Returns the connected *gorm.DB.
func waitForDB(dsn string, timeout time.Duration) (*gorm.DB, error) {
	deadline := time.Now().Add(timeout)
	for {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			if err = db.AutoMigrate(&Log{}); err == nil {
				return db, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		log.Printf("db not ready (%v), retrying in 2s…", err)
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
