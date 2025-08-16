package main

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

const (
	storagePath = "./storage/files"
	tokenFile   = "./tokens.txt"
)

var (
	tokens     = make(map[string]struct{})
	tokensLock sync.RWMutex
)

func main() {
	// создаём storage
	os.MkdirAll(storagePath, 0755)

	// загружаем токены в память
	loadTokens()

	r := gin.Default()
	r.Use(authMiddleware())

	r.POST("/upload", func(c *gin.Context) {
		file, err := c.FormFile("folder")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no file uploaded"})
			return
		}

		os.RemoveAll(storagePath)
		os.MkdirAll(storagePath, 0755)

		// временный файл
		tmpFile, err := os.CreateTemp("", "upload-*.zip")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := c.SaveUploadedFile(file, tmpPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if err := safeUnzip(tmpPath, storagePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "folder replaced"})
	})

	r.GET("/download", func(c *gin.Context) {
		// стримим zip напрямую без промежуточного файла
		c.Header("Content-Disposition", `attachment; filename="folder.zip"`)
		c.Header("Content-Type", "application/zip")

		zipWriter := zip.NewWriter(c.Writer)
		defer zipWriter.Close()

		filepath.Walk(storagePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, _ := filepath.Rel(storagePath, path)
			if info.IsDir() {
				if relPath == "." {
					return nil
				}
				relPath += "/"
			}

			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = relPath
			if !info.IsDir() {
				header.Method = zip.Deflate
			}

			writer, err := zipWriter.CreateHeader(header)
			if err != nil {
				return err
			}

			if !info.IsDir() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				_, err = io.Copy(writer, f)
				return err
			}
			return nil
		})
	})

	r.Run(":1244")
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" || !checkToken(auth) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Next()
	}
}

func loadTokens() {
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return
	}
	tokensLock.Lock()
	defer tokensLock.Unlock()
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			tokens[line] = struct{}{}
		}
	}
}

func checkToken(token string) bool {
	tokensLock.RLock()
	defer tokensLock.RUnlock()
	_, ok := tokens[token]
	return ok
}

func safeUnzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		// защита от zip slip
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return err
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, f.Mode())
			continue
		}
		if err = os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return err
		}
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
