package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/borg001/ipfs-filestorage/internal/auth"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/handler"
	"github.com/borg001/ipfs-filestorage/internal/middleware"
)

func main() {
	fmt.Println("ipfs-filestorage starting...")

	cfg := config.Load()
	handlers := handler.NewHandler(cfg)

	authClient := auth.NewClient(&cfg.Auth)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /file/", handlers.HandleFile)
	mux.HandleFunc("GET /stream/", handlers.HandleStreamMaster)
	mux.HandleFunc("GET /stream/segment/", handlers.HandleStreamSegment)

	mux.HandleFunc("POST /upload", handlers.HandleUpload)
	mux.HandleFunc("POST /upload-multiple", handlers.HandleUploadMultiple)
	mux.HandleFunc("POST /upload-video", handlers.HandleUploadVideo)

	mux.HandleFunc("DELETE /file/", handlers.HandleDelete)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"Not found"}`)
	})

	wrapped := middleware.Chain(
		mux,
		middleware.PanicRecovery(),
		middleware.CORS(cfg.CORS.AllowedOrigins, cfg.CORS.AllowedHeaders),
		middleware.AuthMiddleware(authClient, cfg.API.Keys),
	)

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = cfg.Server.Port
	}

	log.Printf("Server listening on :%s (auth=%s)", port, cfg.Auth.ServiceURL)
	if err := http.ListenAndServe(":"+port, wrapped); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}