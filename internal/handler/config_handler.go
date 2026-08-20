package handler

import (
	"net/http"
	"sort"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

type publicConfigResponse struct {
	Image  imagePublicConfig  `json:"image"`
	Upload uploadPublicConfig `json:"upload"`
}

type imagePublicConfig struct {
	Enabled         bool                   `json:"enabled"`
	Variants        []config.ImageVariant  `json:"variants"`
	PrivacyVariants []privacyVariantConfig `json:"privacy_variants"`
	OutputFormat    string                 `json:"output_format"`
	JPEGProgressive bool                   `json:"jpeg_progressive"`
	ResizePolicy    string                 `json:"resize_policy"`
	URLTemplate     string                 `json:"url_template"`
	BundleTemplate  string                 `json:"bundle_template"`
}

type privacyVariantConfig struct {
	Key      string `json:"key"`
	Purpose  string `json:"purpose"`
	Fallback string `json:"fallback,omitempty"`
}

// uploadPublicConfig exposes only client-facing media limits. It deliberately
// does not expose storage paths, IPFS topology or authentication settings.
type uploadPublicConfig struct {
	Media mediaUploadPublicConfig `json:"media"`
}

type mediaUploadPublicConfig struct {
	Image mediaUploadPolicy `json:"image"`
	Video videoUploadPolicy `json:"video"`
}

type mediaUploadPolicy struct {
	Accept          string   `json:"accept"`
	MimeTypes       []string `json:"mime_types"`
	MaxBytes        int64    `json:"max_bytes"`
	MaxSizeLabel    string   `json:"max_size_label"`
	Description     string   `json:"description"`
	TooLargeMessage string   `json:"too_large_message"`
}

type videoUploadPolicy struct {
	mediaUploadPolicy
	MaxDurationSec      int    `json:"max_duration_sec"`
	ExpectedAspectRatio string `json:"expected_aspect_ratio"`
}

func (h *Handler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	locale := uploadLocale(r)
	imageTypes := mediaImageMimeTypes(h.cfg.Upload.AllowedMimeTypes)
	videoTypes := []string{"video/mp4", "video/quicktime", "video/webm", "video/x-msvideo", "video/x-matroska"}
	imageMax := humanFileSize(h.cfg.Upload.MaxFileSize, locale)
	videoMax := humanFileSize(h.cfg.Video.MaxSizeBytes, locale)
	videoDuration := humanDuration(h.cfg.Video.MaxDurationSec, locale)
	imageDescription := "JPEG, PNG, WebP up to " + imageMax
	videoDescription := "MP4, MOV, WebM, AVI, MKV up to " + videoMax + ", up to " + videoDuration + ", vertical 9:16"
	imageTooLarge := "The file exceeds the " + imageMax + " limit."
	videoTooLarge := "The file exceeds the " + videoMax + " limit."
	if locale == "ru" {
		imageDescription = "JPEG, PNG, WebP до " + imageMax
		videoDescription = "MP4, MOV, WebM, AVI, MKV до " + videoMax + ", до " + videoDuration + ", вертикальное 9:16"
		imageTooLarge = "Размер файла превышает допустимые " + imageMax + "."
		videoTooLarge = "Размер файла превышает допустимые " + videoMax + "."
	}
	writeJSON(w, http.StatusOK, publicConfigResponse{
		Image: imagePublicConfig{
			Enabled:  h.cfg.Image.ProcessingEnabled,
			Variants: h.cfg.Image.Variants,
			PrivacyVariants: []privacyVariantConfig{
				{Key: config.PrivacyBlurVariantKey, Purpose: "full_image_blur"},
				{Key: config.PrivacyFaceBlurVariantKey, Purpose: "detected_faces_blur", Fallback: config.PrivacyBlurVariantKey},
			},
			OutputFormat:    h.cfg.Image.OutputFormat,
			JPEGProgressive: h.cfg.Image.JPEGProgressive,
			ResizePolicy:    h.cfg.Image.ResizePolicy,
			URLTemplate:     "/file/{cid}/{size}",
			BundleTemplate:  "/file/{cid}/bundle",
		},
		Upload: uploadPublicConfig{Media: mediaUploadPublicConfig{
			Image: mediaUploadPolicy{
				Accept:          strings.Join(imageTypes, ","),
				MimeTypes:       imageTypes,
				MaxBytes:        h.cfg.Upload.MaxFileSize,
				MaxSizeLabel:    imageMax,
				Description:     imageDescription,
				TooLargeMessage: imageTooLarge,
			},
			Video: videoUploadPolicy{
				mediaUploadPolicy: mediaUploadPolicy{
					Accept:          strings.Join(append(videoTypes, ".mp4", ".mov", ".webm", ".avi", ".mkv"), ","),
					MimeTypes:       videoTypes,
					MaxBytes:        h.cfg.Video.MaxSizeBytes,
					MaxSizeLabel:    videoMax,
					Description:     videoDescription,
					TooLargeMessage: videoTooLarge,
				},
				MaxDurationSec:      h.cfg.Video.MaxDurationSec,
				ExpectedAspectRatio: "9:16",
			},
		}},
	})
}

func mediaImageMimeTypes(all map[string]bool) []string {
	allowed := map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}
	types := make([]string, 0, len(allowed))
	for mimeType := range allowed {
		if all[mimeType] {
			types = append(types, mimeType)
		}
	}
	sort.Strings(types)
	return types
}
