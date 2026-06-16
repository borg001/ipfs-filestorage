package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/auth/lua"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/handler"
	"github.com/borg001/ipfs-filestorage/internal/middleware"
)

func main() {
	fmt.Println("ipfs-filestorage starting...")

	cfg := config.Load()
	handlers := handler.NewHandler(cfg)

	// Initialize Lua auth provider if configured
	var luaProvider *lua.Provider
	if cfg.Auth.LuaScript != "" {
		luaScript := cfg.Auth.LuaScript
		if strings.HasSuffix(luaScript, ".lua") || strings.HasPrefix(luaScript, "/") || strings.HasPrefix(luaScript, "./") {
			data, err := os.ReadFile(luaScript)
			if err != nil {
				log.Fatalf("[AUTH] Failed to read Lua script %q: %v", luaScript, err)
			}
			luaScript = string(data)
		}
		envWhitelist := make(map[string]string)
		for _, key := range strings.Split(cfg.Auth.LuaEnvWhitelist, ",") {
			if k := strings.TrimSpace(key); k != "" {
				envWhitelist[k] = os.Getenv(k)
			}
		}
		luaProvider = lua.NewProvider(
			luaScript,
			cfg.Auth.LuaTimeoutMs,
			cfg.Auth.LuaMaxMemoryMB,
			envWhitelist,
			splitCSV(cfg.Auth.LuaAllowedPrivateHosts),
		)
		log.Printf("[AUTH] Lua script configured (timeout=%dms, maxMemory=%dMB)",
			cfg.Auth.LuaTimeoutMs, cfg.Auth.LuaMaxMemoryMB)
	} else {
		log.Println("[AUTH] No Lua script configured — static API keys only")
	}

	mux := http.NewServeMux()

	// File upload/download handlers
	mux.HandleFunc("POST /upload", handlers.HandleUpload)
	mux.HandleFunc("POST /upload-multiple", handlers.HandleUploadMultiple)
	mux.HandleFunc("GET /file/", handlers.HandleFile)
	mux.HandleFunc("DELETE /file/", handlers.HandleDelete)

	// Video streaming handlers
	mux.HandleFunc("POST /upload-video", handlers.HandleUploadVideo)
	mux.HandleFunc("GET /stream/", handlers.HandleStreamMaster)
	mux.HandleFunc("GET /stream/segment/", handlers.HandleStreamSegment)

	// Default
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"Not found"}`)
	})

	wrapped := middleware.Chain(
		mux,
		middleware.PanicRecovery(),
		middleware.SecurityHeaders(),
		middleware.RateLimit(middleware.RateLimitConfig{
			RPS:   cfg.RateLimit.RPS,
			Burst: cfg.RateLimit.Burst,
		}),
		middleware.AuthMiddleware(cfg.API.Keys, luaProvider),
		middleware.CORS(cfg.CORS.AllowedOrigins, cfg.CORS.AllowedHeaders),
	)

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = cfg.Server.Port
	}

	log.Printf("Server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, wrapped); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if v := strings.TrimSpace(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}
