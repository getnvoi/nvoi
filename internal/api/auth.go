package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type contextKey string

const contextKeyUser contextKey = "user"

// AuthRequired verifies the JWT Bearer token and loads the user.
func AuthRequired(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		tokenStr := strings.TrimPrefix(header, "Bearer ")
		if tokenStr == header {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format, use: Bearer <token>"})
			return
		}

		claims, err := VerifyToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		var user User
		if err := db.First(&user, "id = ?", claims.UserID).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
			return
		}

		c.Set(string(contextKeyUser), &user)
		// Also store on request context so huma handlers can access it.
		ctx := context.WithValue(c.Request.Context(), contextKeyUser, &user)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// CurrentUser returns the authenticated user from a gin.Context (for Gin handlers).
func CurrentUser(c *gin.Context) *User {
	u, _ := c.Get(string(contextKeyUser))
	return u.(*User)
}

// UserFromContext returns the authenticated user from a context.Context (for huma handlers).
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(contextKeyUser).(*User)
	return u
}
