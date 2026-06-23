package handler

import (
	"net/http"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

type publicConfigResponse struct {
	Image imagePublicConfig `json:"image"`
}

type imagePublicConfig struct {
	Enabled         bool                  `json:"enabled"`
	Variants        []config.ImageVariant `json:"variants"`
	OutputFormat    string                `json:"output_format"`
	JPEGProgressive bool                  `json:"jpeg_progressive"`
	ResizePolicy    string                `json:"resize_policy"`
	URLTemplate     string                `json:"url_template"`
	BundleTemplate  string                `json:"bundle_template"`
}

func (h *Handler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, publicConfigResponse{
		Image: imagePublicConfig{
			Enabled:         h.cfg.Image.ProcessingEnabled,
			Variants:        h.cfg.Image.Variants,
			OutputFormat:    h.cfg.Image.OutputFormat,
			JPEGProgressive: h.cfg.Image.JPEGProgressive,
			ResizePolicy:    h.cfg.Image.ResizePolicy,
			URLTemplate:     "/file/{cid}/{size}",
			BundleTemplate:  "/file/{cid}/bundle",
		},
	})
}
