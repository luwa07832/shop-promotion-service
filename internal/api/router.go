package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/luwa07832/shop-promotion-service/internal/store"
)

// NewRouter wires the public HTTP surface. Only the health entry is published today; the service
// contract in README.md describes the error shape every entry must keep.
func NewRouter(st *store.Store) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.RedirectTrailingSlash = false
	router.Use(gin.Recovery())

	router.POST("/v1/promotions", createPromotion(st))
	router.GET("/v1/promotions/:id", getPromotion(st))

	router.GET("/healthz", func(c *gin.Context) {
		if err := st.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "storage_unavailable", "message": "database is not available"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "database": "ok"})
	})

	router.NoRoute(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/v1/promotions/" {
			writeAPIError(c, http.StatusNotFound, "promotion_not_found", "no promotion exists for the given id")
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "route_not_found", "message": "no route matches this path"}})
	})
	return router
}
