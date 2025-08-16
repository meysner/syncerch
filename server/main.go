package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

type Config struct {
	StoragePath        string
	TokenFile          string
	Port               string
	MaxMultipartMemory int64 // bytes
	MaxUploadBytes     int64 // bytes (лимит всего запроса), 0 = без лимита
}

var (
	tokens     = make(map[string]struct{})
	tokensLock sync.RWMutex

	storeLock sync.RWMutex // защищает операции чтения/записи каталога storage
)

func main() {
	cfg := loadConfig()

	if err := os.MkdirAll(cfg.StoragePath, 0755); err != nil {
		log.Fatalf("failed to create storage dir %s: %v", cfg.StoragePath, err)
	}

	// Загружаем токены и запускаем их авто‑перезагрузку
	loadTokens(cfg.TokenFile)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go watchTokens(ctx, cfg.TokenFile, 5*time.Second)

	// Gin в release‑режиме по умолчанию
	if gin.Mode() == gin.DebugMode && os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Ограничение памяти multipart (чтобы не держать файл в RAM)
	r.MaxMultipartMemory = cfg.MaxMultipartMemory

	// Health‑эндпоинты без авторизации
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/readyz", func(c *gin.Context) {
		if _, err := os.Stat(cfg.StoragePath); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "storage not ready", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	// Авторизация на остальные пути
	r.Use(authMiddleware())

	r.POST("/upload", func(c *gin.Context) {
		// Лимит на общий объём запроса (включая заголовки/части multipart)
		if cfg.MaxUploadBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, cfg.MaxUploadBytes)
		}

		file, err := c.FormFile("folder")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no file uploaded"})
			return
		}

		storeLock.Lock()
		defer storeLock.Unlock()

		// Чистим storage (безопасность: не позволяем удалить /)
		if err := safeCleanDir(cfg.StoragePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := os.MkdirAll(cfg.StoragePath, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Временный файл рядом со storage
		tmpFile, err := os.CreateTemp(cfg.StoragePath, "upload-*.zip")
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

		if err := safeUnzip(tmpPath, cfg.StoragePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "folder replaced"})
	})

	r.GET("/download", func(c *gin.Context) {
		storeLock.RLock()
		defer storeLock.RUnlock()

		c.Header("Content-Disposition", `attachment; filename="folder.zip"`)
		c.Header("Content-Type", "application/zip")
		c.Header("Cache-Control", "no-store")

		zipWriter := zip.NewWriter(c.Writer)
		defer zipWriter.Close()

		err := filepath.Walk(cfg.StoragePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, _ := filepath.Rel(cfg.StoragePath, path)
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
				_, copyErr := io.Copy(writer, f)
				closeErr := f.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
			}
			return nil
		})

		if err != nil {
			// Уже начали стримить, меняем только статус в логах
			log.Printf("download error: %v", err)
		}
	})

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		log.Printf("listening on :%s, storage=%s, tokens=%s", cfg.Port, cfg.StoragePath, cfg.TokenFile)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	log.Println("bye")
}

func loadConfig() Config {
	return Config{
		StoragePath:        getEnv("STORAGE_PATH", "/data"),
		TokenFile:          getEnv("TOKENS_PATH", "/run/secrets/tokens.txt"),
		Port:               getEnv("PORT", "1244"),
		MaxMultipartMemory: getEnvBytes("MAX_MULTIPART_MB", 8) * 1024 * 1024,
		MaxUploadBytes:     getEnvBytes("MAX_UPLOAD_MB", 0) * 1024 * 1024, // 0 = без лимита
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvBytes(key string, defMB int64) int64 {
	if v := os.Getenv(key); v != "" {
		if mb, err := parseInt64(v); err == nil {
			return mb
		}
	}
	return defMB
}

func parseInt64(s string) (int64, error) {
	var x int64
	_, err := fmt.Sscan(s, &x)
	return x, err
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

func loadTokens(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("no tokens loaded from %s: %v", path, err)
		return
	}
	newMap := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			newMap[line] = struct{}{}
		}
	}
	tokensLock.Lock()
	tokens = newMap
	tokensLock.Unlock()
	log.Printf("tokens loaded: %d", len(newMap))
}

func watchTokens(ctx context.Context, path string, every time.Duration) {
	if every <= 0 {
		every = 5 * time.Second
	}
	var lastMod time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
			fi, err := os.Stat(path)
			if err != nil {
				continue
			}
			if fi.ModTime().After(lastMod) {
				lastMod = fi.ModTime()
				loadTokens(path)
			}
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

	cleanDest := filepath.Clean(dest)

	for _, f := range r.File {
		fpath := filepath.Join(cleanDest, f.Name)

		// Защита от zip slip
		rel, err := filepath.Rel(cleanDest, fpath)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("zip slip detected: entry %q escapes %q", f.Name, dest)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fpath, f.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			_ = outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		cerr1 := rc.Close()
		cerr2 := outFile.Close()
		if err != nil {
			return err
		}
		if cerr1 != nil {
			return cerr1
		}
		if cerr2 != nil {
			return cerr2
		}
	}
	return nil
}
func safeCleanDir(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if abs == "/" || abs == "." {
		return fmt.Errorf("refusing to clean unsafe path: %s", abs)
	}
	// гарантируем, что каталог есть
	if err := os.MkdirAll(abs, 0755); err != nil {
		return err
	}
	// удаляем только содержимое
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(abs, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
