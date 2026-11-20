package handler

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nowen-reader/nowen-reader/internal/service"
	"github.com/nowen-reader/nowen-reader/internal/store"
)

type remoteOPDSRequest struct {
	URL       string `json:"url" binding:"required"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	LibraryID string `json:"libraryId"`
	RootPath  string `json:"rootPath"`
}

func bindRemoteOPDS(c *gin.Context, input *remoteOPDSRequest) bool {
	c.Header("Cache-Control", "no-store")
	if c.ContentType() != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "Expected application/json"})
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid catalog request"})
		return false
	}
	return true
}

func readRemoteOPDS(c *gin.Context) {
	var input remoteOPDSRequest
	if !bindRemoteOPDS(c, &input) {
		return
	}
	catalog, err := service.FetchRemoteOPDS(c.Request.Context(), strings.TrimSpace(input.URL), input.Username, input.Password)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, catalog)
}

func importRemoteOPDS(c *gin.Context) {
	var input remoteOPDSRequest
	if !bindRemoteOPDS(c, &input) {
		return
	}
	library, err := store.GetLibraryByID(input.LibraryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query library"})
		return
	}
	if library == nil || !library.Enabled || !configuredOPDSRoot(input.RootPath, library.RootPath, library.RootPaths) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Select a configured folder in an enabled library"})
		return
	}
	ownership, err := service.LoadLibraryOwnership()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check library folder ownership"})
		return
	}
	if ownership.RootHasExactConflict(input.RootPath) || !ownership.IsOwnedBy(library.ID, input.RootPath) {
		c.JSON(http.StatusConflict, gin.H{"error": "Selected folder conflicts with another library"})
		return
	}
	if info, err := os.Stat(input.RootPath); err != nil || !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Selected library folder is unavailable"})
		return
	}
	name, err := service.ImportRemoteOPDS(c.Request.Context(), strings.TrimSpace(input.URL), input.Username, input.Password, input.RootPath, func(name string) bool {
		return isFileAllowedForLibraryType(name, library.Type)
	})
	if errors.Is(err, os.ErrExist) {
		c.JSON(http.StatusConflict, gin.H{"error": "A file with this name already exists"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_, _, scanErr := service.SyncLibraryByID(library.ID)
	c.JSON(http.StatusCreated, gin.H{"filename": name, "scanCompleted": scanErr == nil})
}

func configuredOPDSRoot(root, primary string, others []string) bool {
	if root == "" {
		return false
	}
	if root == primary {
		return true
	}
	for _, candidate := range others {
		if root == candidate {
			return true
		}
	}
	return false
}
